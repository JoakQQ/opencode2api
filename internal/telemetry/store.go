package telemetry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Store retains the metrics and log events the console shows in a SQLite
// database, so a restart no longer resets them. Rows older than
// RetentionHours are pruned; the lifetime totals are kept.
type Store struct {
	db   *sql.DB
	path string
	ops  chan func(*sql.Tx) error
	done chan struct{}
	// closed guards the ops channel against a send after Close.
	mu     sync.RWMutex
	closed bool
	// dropped counts writes discarded because the queue was full or the store
	// was closed; the console never blocks a request on disk I/O.
	dropped atomic.Uint64
	// lastError keeps the most recent write failure for the configuration page.
	lastError atomic.Pointer[string]
}

// RetentionHours bounds how long metrics and log events are kept. Older rows
// are deleted so the database file stays small.
const RetentionHours = 48

// storeBatchSize is how many queued writes are committed in one transaction.
const storeBatchSize = 256

// storeFlushInterval bounds how long a queued write waits before it is
// committed.
const storeFlushInterval = 500 * time.Millisecond

// OpenStore opens (creating it if needed) the SQLite database at path, applies
// the schema and prunes rows older than RetentionHours.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("storage.path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", absolute)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", absolute, err)
	}
	// One connection serializes the writer with the startup and prune reads,
	// which keeps SQLite from reporting the database as busy.
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: absolute, ops: make(chan func(*sql.Tx) error, storeBatchSize*16), done: make(chan struct{})}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.prune(); err != nil {
		_ = db.Close()
		return nil, err
	}
	go store.run()
	return store, nil
}

// Path is the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Failing reports the most recent write error, if any, for the configuration
// page and the startup log.
func (s *Store) Failing() error {
	if message := s.lastError.Load(); message != nil {
		return errors.New(*message)
	}
	return nil
}

// Dropped is the number of writes discarded because the queue was full or the
// store had been closed.
func (s *Store) Dropped() uint64 { return s.dropped.Load() }

func (s *Store) migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS metric_minutes (
			minute INTEGER PRIMARY KEY,
			total INTEGER NOT NULL, success INTEGER NOT NULL, errors INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL, histogram TEXT NOT NULL,
			endpoints TEXT NOT NULL, models TEXT NOT NULL, tiers TEXT NOT NULL, statuses TEXT NOT NULL,
			usage_requests INTEGER NOT NULL, usage_reported INTEGER NOT NULL,
			tokens_input INTEGER NOT NULL, tokens_output INTEGER NOT NULL, tokens_cached INTEGER NOT NULL,
			tokens_reasoning INTEGER NOT NULL, tokens_total INTEGER NOT NULL,
			usage_models TEXT NOT NULL, usage_tiers TEXT NOT NULL, usage_sessions TEXT NOT NULL, usage_keys TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS upstream_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time INTEGER NOT NULL, request_id TEXT NOT NULL, session TEXT NOT NULL,
			server_key_id TEXT NOT NULL, model TEXT NOT NULL, tier TEXT NOT NULL, key_id TEXT NOT NULL,
			channel TEXT NOT NULL, anonymous INTEGER NOT NULL, proxy TEXT NOT NULL,
			attempts INTEGER NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
			success INTEGER NOT NULL, outcome TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS upstream_attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time INTEGER NOT NULL, request_id TEXT NOT NULL, model TEXT NOT NULL, tier TEXT NOT NULL,
			attempt INTEGER NOT NULL, key_id TEXT NOT NULL, channel TEXT NOT NULL,
			anonymous INTEGER NOT NULL, proxy TEXT NOT NULL, status INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL, success INTEGER NOT NULL, outcome TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS log_events (
			sequence INTEGER PRIMARY KEY,
			time INTEGER NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL,
			component TEXT NOT NULL, fields TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS lifetime_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			usage TEXT NOT NULL, attempts TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0, success INTEGER NOT NULL DEFAULT 0, errors INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS upstream_requests_time ON upstream_requests (time)`,
		`CREATE INDEX IF NOT EXISTS upstream_attempts_time ON upstream_attempts (time)`,
		`CREATE INDEX IF NOT EXISTS log_events_time ON log_events (time)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("apply storage schema: %w", err)
		}
	}
	return nil
}

// prune deletes every row older than RetentionHours. The lifetime totals are
// deliberately kept: they are the only counters that outlive the window.
func (s *Store) prune() error {
	cutoff := time.Now().Add(-RetentionHours * time.Hour)
	minute := cutoff.Unix() / 60
	millis := cutoff.UnixMilli()
	for _, statement := range []struct {
		query string
		value int64
	}{
		{"DELETE FROM metric_minutes WHERE minute < ?", minute},
		{"DELETE FROM upstream_requests WHERE time < ?", millis},
		{"DELETE FROM upstream_attempts WHERE time < ?", millis},
		{"DELETE FROM log_events WHERE time < ?", millis},
	} {
		if _, err := s.db.Exec(statement.query, statement.value); err != nil {
			return fmt.Errorf("prune storage: %w", err)
		}
	}
	return nil
}

// enqueue queues one write. It never blocks: a full queue or a closed store
// drops the write and bumps the dropped counter.
func (s *Store) enqueue(op func(*sql.Tx) error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.dropped.Add(1)
		return
	}
	select {
	case s.ops <- op:
	default:
		s.dropped.Add(1)
	}
}

// run commits queued writes in batches and prunes expired rows hourly.
func (s *Store) run() {
	defer close(s.done)
	batch := make([]func(*sql.Tx) error, 0, storeBatchSize)
	flushTicker := time.NewTicker(storeFlushInterval)
	defer flushTicker.Stop()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.writeBatch(batch); err != nil {
			message := err.Error()
			s.lastError.Store(&message)
		}
		batch = batch[:0]
	}
	for {
		select {
		case op, ok := <-s.ops:
			if !ok {
				flush()
				return
			}
			batch = append(batch, op)
			if len(batch) >= storeBatchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-pruneTicker.C:
			flush()
			if err := s.prune(); err != nil {
				message := err.Error()
				s.lastError.Store(&message)
			}
		}
	}
}

func (s *Store) writeBatch(batch []func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin storage transaction: %w", err)
	}
	for _, op := range batch {
		if err := op(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("write storage: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage: %w", err)
	}
	return nil
}

// Close drains the queue, prunes once more and closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.ops)
	s.mu.Unlock()
	<-s.done
	return s.db.Close()
}

const minuteSelect = `SELECT minute, total, success, errors, duration_ms, histogram, endpoints, models, tiers, statuses,
	usage_requests, usage_reported, tokens_input, tokens_output, tokens_cached, tokens_reasoning, tokens_total,
	usage_models, usage_tiers, usage_sessions, usage_keys FROM metric_minutes`

// loadMinutes reads the minute buckets starting at from (in unix minutes),
// oldest first.
func (s *Store) loadMinutes(from int64) ([]metricBucket, error) {
	rows, err := s.db.Query(minuteSelect+` WHERE minute >= ? ORDER BY minute`, from)
	if err != nil {
		return nil, fmt.Errorf("read metric minutes: %w", err)
	}
	defer rows.Close()
	var buckets []metricBucket
	for rows.Next() {
		var (
			bucket                                       metricBucket
			histogram, endpoints, models, tiers, status  string
			usageModels, usageTiers, usageSessions, keys string
		)
		if err := rows.Scan(&bucket.minute, &bucket.total, &bucket.success, &bucket.errors, &bucket.duration, &histogram, &endpoints, &models, &tiers, &status,
			&bucket.usageRequests, &bucket.usageReported, &bucket.tokens.Input, &bucket.tokens.Output, &bucket.tokens.Cached, &bucket.tokens.Reasoning, &bucket.tokens.Total,
			&usageModels, &usageTiers, &usageSessions, &keys); err != nil {
			return nil, fmt.Errorf("read metric minute: %w", err)
		}
		bucket.endpoints, bucket.models, bucket.tiers, bucket.statuses = map[string]uint64{}, map[string]uint64{}, map[string]uint64{}, map[string]uint64{}
		bucket.usageModels, bucket.usageTiers, bucket.usageSessions = map[string]TokenCounts{}, map[string]TokenCounts{}, map[string]TokenCounts{}
		bucket.usageKeys = map[string]ServerKeyUsage{}
		for _, field := range []struct {
			raw    string
			target any
		}{
			{histogram, &bucket.histogram}, {endpoints, &bucket.endpoints}, {models, &bucket.models},
			{tiers, &bucket.tiers}, {status, &bucket.statuses}, {usageModels, &bucket.usageModels},
			{usageTiers, &bucket.usageTiers}, {usageSessions, &bucket.usageSessions}, {keys, &bucket.usageKeys},
		} {
			if err := json.Unmarshal([]byte(field.raw), field.target); err != nil {
				return nil, fmt.Errorf("decode metric minute: %w", err)
			}
		}
		buckets = append(buckets, bucket)
	}
	return buckets, rows.Err()
}

// lifetimeTotals is the persisted state that outlives the retention window:
// the usage and attempt aggregates plus the request counters.
type lifetimeTotals struct {
	Usage   UsagePeriod
	Attempt AttemptAggregate
	Total   uint64
	Success uint64
	Errors  uint64
}

// loadLifetime reads the persisted lifetime totals. ok is false when the store
// has never flushed them.
func (s *Store) loadLifetime() (totals lifetimeTotals, ok bool, err error) {
	var usageJSON, attemptsJSON string
	err = s.db.QueryRow(`SELECT usage, attempts, requests, success, errors FROM lifetime_state WHERE id = 1`).
		Scan(&usageJSON, &attemptsJSON, &totals.Total, &totals.Success, &totals.Errors)
	if errors.Is(err, sql.ErrNoRows) {
		return lifetimeTotals{Usage: usagePeriod(), Attempt: attemptAggregate()}, false, nil
	}
	if err != nil {
		return lifetimeTotals{}, false, fmt.Errorf("read lifetime totals: %w", err)
	}
	totals.Usage, totals.Attempt = usagePeriod(), attemptAggregate()
	if err := json.Unmarshal([]byte(usageJSON), &totals.Usage); err != nil {
		return lifetimeTotals{}, false, fmt.Errorf("decode lifetime usage: %w", err)
	}
	if err := json.Unmarshal([]byte(attemptsJSON), &totals.Attempt); err != nil {
		return lifetimeTotals{}, false, fmt.Errorf("decode lifetime attempts: %w", err)
	}
	totals.Usage.Models, totals.Usage.Tiers = nonNilTokens(totals.Usage.Models), nonNilTokens(totals.Usage.Tiers)
	totals.Usage.Sessions, totals.Usage.ServerKeys = nonNilTokens(totals.Usage.Sessions), nonNilKeys(totals.Usage.ServerKeys)
	totals.Attempt.Tiers, totals.Attempt.Channels = nonNilAttempts(totals.Attempt.Tiers), nonNilAttempts(totals.Attempt.Channels)
	totals.Attempt.Keys = nonNilAttempts(totals.Attempt.Keys)
	return totals, true, nil
}

// loadRequests returns the newest limit requests not older than since, oldest
// first.
func (s *Store) loadRequests(since time.Time, limit int) ([]UpstreamRequest, error) {
	rows, err := s.db.Query(`SELECT time, request_id, session, server_key_id, model, tier, key_id, channel,
		anonymous, proxy, attempts, status, duration_ms, success, outcome
		FROM upstream_requests WHERE time >= ? ORDER BY time DESC, id DESC LIMIT ?`, since.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("read requests: %w", err)
	}
	defer rows.Close()
	var requests []UpstreamRequest
	for rows.Next() {
		var (
			request   UpstreamRequest
			timeValue int64
			anonymous int
			success   int
		)
		if err := rows.Scan(&timeValue, &request.RequestID, &request.Session, &request.ServerKeyID, &request.Model, &request.Tier,
			&request.KeyID, &request.Channel, &anonymous, &request.Proxy, &request.Attempts, &request.Status,
			&request.DurationMS, &success, &request.Outcome); err != nil {
			return nil, fmt.Errorf("read request: %w", err)
		}
		request.Time = time.UnixMilli(timeValue).UTC()
		request.Anonymous = anonymous != 0
		request.Success = success != 0
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Oldest first is the order the monitor's recent buffer holds.
	for i, j := 0, len(requests)-1; i < j; i, j = i+1, j-1 {
		requests[i], requests[j] = requests[j], requests[i]
	}
	return requests, nil
}

// loadAttempts returns the newest limit attempts not older than since, oldest
// first.
func (s *Store) loadAttempts(since time.Time, limit int) ([]UpstreamAttempt, error) {
	rows, err := s.db.Query(`SELECT time, request_id, model, tier, attempt, key_id, channel, anonymous, proxy,
		status, duration_ms, success, outcome FROM upstream_attempts WHERE time >= ?
		ORDER BY time DESC, id DESC LIMIT ?`, since.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("read attempts: %w", err)
	}
	defer rows.Close()
	var attempts []UpstreamAttempt
	for rows.Next() {
		var (
			attempt   UpstreamAttempt
			timeValue int64
			anonymous int
			success   int
		)
		if err := rows.Scan(&timeValue, &attempt.RequestID, &attempt.Model, &attempt.Tier, &attempt.Attempt, &attempt.KeyID,
			&attempt.Channel, &anonymous, &attempt.Proxy, &attempt.Status, &attempt.DurationMS, &success, &attempt.Outcome); err != nil {
			return nil, fmt.Errorf("read attempt: %w", err)
		}
		attempt.Time = time.UnixMilli(timeValue).UTC()
		attempt.Anonymous = anonymous != 0
		attempt.Success = success != 0
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(attempts)-1; i < j; i, j = i+1, j-1 {
		attempts[i], attempts[j] = attempts[j], attempts[i]
	}
	return attempts, nil
}

// loadLogs returns the newest limit log events, oldest first.
func (s *Store) loadLogs(limit int) ([]LogEvent, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT sequence, time, level, message, component, fields FROM log_events
		ORDER BY sequence DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read log events: %w", err)
	}
	defer rows.Close()
	var events []LogEvent
	for rows.Next() {
		var (
			event     LogEvent
			timeValue int64
			fields    string
		)
		if err := rows.Scan(&event.Sequence, &timeValue, &event.Level, &event.Message, &event.Component, &fields); err != nil {
			return nil, fmt.Errorf("read log event: %w", err)
		}
		event.Time = time.UnixMilli(timeValue).UTC()
		if fields != "" && fields != "{}" {
			event.Fields = make(map[string]any)
			if err := json.Unmarshal([]byte(fields), &event.Fields); err != nil {
				return nil, fmt.Errorf("decode log fields: %w", err)
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

func nonNilTokens(source map[string]TokenCounts) map[string]TokenCounts {
	if source == nil {
		return make(map[string]TokenCounts)
	}
	return source
}

func nonNilKeys(source map[string]ServerKeyUsage) map[string]ServerKeyUsage {
	if source == nil {
		return make(map[string]ServerKeyUsage)
	}
	return source
}

func nonNilAttempts(source map[string]AttemptCounts) map[string]AttemptCounts {
	if source == nil {
		return make(map[string]AttemptCounts)
	}
	return source
}

// persistMinute queues the upsert of one minute bucket.
func (s *Store) persistMinute(bucket metricBucket) {
	histogram, _ := json.Marshal(bucket.histogram)
	endpoints, _ := json.Marshal(bucket.endpoints)
	models, _ := json.Marshal(bucket.models)
	tiers, _ := json.Marshal(bucket.tiers)
	statuses, _ := json.Marshal(bucket.statuses)
	usageModels, _ := json.Marshal(bucket.usageModels)
	usageTiers, _ := json.Marshal(bucket.usageTiers)
	usageSessions, _ := json.Marshal(bucket.usageSessions)
	usageKeys, _ := json.Marshal(bucket.usageKeys)
	s.enqueue(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO metric_minutes (
			minute, total, success, errors, duration_ms, histogram, endpoints, models, tiers, statuses,
			usage_requests, usage_reported, tokens_input, tokens_output, tokens_cached, tokens_reasoning, tokens_total,
			usage_models, usage_tiers, usage_sessions, usage_keys)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(minute) DO UPDATE SET
			total=excluded.total, success=excluded.success, errors=excluded.errors, duration_ms=excluded.duration_ms,
			histogram=excluded.histogram, endpoints=excluded.endpoints, models=excluded.models, tiers=excluded.tiers,
			statuses=excluded.statuses, usage_requests=excluded.usage_requests, usage_reported=excluded.usage_reported,
			tokens_input=excluded.tokens_input, tokens_output=excluded.tokens_output, tokens_cached=excluded.tokens_cached,
			tokens_reasoning=excluded.tokens_reasoning, tokens_total=excluded.tokens_total,
			usage_models=excluded.usage_models, usage_tiers=excluded.usage_tiers,
			usage_sessions=excluded.usage_sessions, usage_keys=excluded.usage_keys`,
			bucket.minute, bucket.total, bucket.success, bucket.errors, bucket.duration, string(histogram), string(endpoints),
			string(models), string(tiers), string(statuses), bucket.usageRequests, bucket.usageReported,
			bucket.tokens.Input, bucket.tokens.Output, bucket.tokens.Cached, bucket.tokens.Reasoning, bucket.tokens.Total,
			string(usageModels), string(usageTiers), string(usageSessions), string(usageKeys))
		return err
	})
}

// persistLifetime queues the upsert of the lifetime totals.
func (s *Store) persistLifetime(totals lifetimeTotals) {
	usageJSON, err := json.Marshal(totals.Usage)
	if err != nil {
		return
	}
	attemptsJSON, err := json.Marshal(totals.Attempt)
	if err != nil {
		return
	}
	s.enqueue(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO lifetime_state (id, usage, attempts, requests, success, errors) VALUES (1, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET usage=excluded.usage, attempts=excluded.attempts,
			requests=excluded.requests, success=excluded.success, errors=excluded.errors`,
			string(usageJSON), string(attemptsJSON), totals.Total, totals.Success, totals.Errors)
		return err
	})
}

// persistRequest queues one client request row.
func (s *Store) persistRequest(request UpstreamRequest) {
	s.enqueue(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO upstream_requests (time, request_id, session, server_key_id, model, tier, key_id,
			channel, anonymous, proxy, attempts, status, duration_ms, success, outcome)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			request.Time.UnixMilli(), request.RequestID, request.Session, request.ServerKeyID, request.Model, request.Tier,
			request.KeyID, request.Channel, boolInt(request.Anonymous), request.Proxy, request.Attempts, request.Status,
			request.DurationMS, boolInt(request.Success), request.Outcome)
		return err
	})
}

// persistAttempt queues one upstream attempt row.
func (s *Store) persistAttempt(attempt UpstreamAttempt) {
	s.enqueue(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO upstream_attempts (time, request_id, model, tier, attempt, key_id, channel,
			anonymous, proxy, status, duration_ms, success, outcome) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			attempt.Time.UnixMilli(), attempt.RequestID, attempt.Model, attempt.Tier, attempt.Attempt, attempt.KeyID,
			attempt.Channel, boolInt(attempt.Anonymous), attempt.Proxy, attempt.Status, attempt.DurationMS,
			boolInt(attempt.Success), attempt.Outcome)
		return err
	})
}

// persistLog queues one log event under the sequence the broker assigned.
func (s *Store) persistLog(event LogEvent) {
	fields := "{}"
	if len(event.Fields) > 0 {
		if encoded, err := json.Marshal(event.Fields); err == nil {
			fields = string(encoded)
		}
	}
	s.enqueue(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT OR REPLACE INTO log_events (sequence, time, level, message, component, fields)
			VALUES (?,?,?,?,?,?)`, event.Sequence, event.Time.UnixMilli(), event.Level, event.Message, event.Component, fields)
		return err
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
