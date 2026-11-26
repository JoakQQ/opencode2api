package telemetry

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	wire "opencode2api/internal/protocol"
)

// recordUsage accounts one finished request whose route reported usage.
func recordUsage(t *testing.T, metrics *Metrics, name string, protocol wire.Protocol, usage wire.Usage) {
	t.Helper()
	metrics.Record("/"+name, 200, 5*time.Millisecond, &RequestMeta{
		Model: name, Tier: "go", Protocol: protocol, Request: "req_" + name, Usage: usage, UsageReported: true,
	})
}

// TestUsageSplitByProtocol checks the console's chat/embeddings split: the
// window, its trend buckets and the lifetime totals all attribute tokens to the
// protocol family that served them, and the families sum to the total.
func TestUsageSplitByProtocol(t *testing.T) {
	metrics, err := Monitor(nil)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	recordUsage(t, metrics, "gpt-5", wire.Chat, wire.Usage{Input: 100, Output: 20, Total: 120})
	recordUsage(t, metrics, "gpt-5", wire.Responses, wire.Usage{Input: 50, Output: 10, Total: 60})
	recordUsage(t, metrics, "claude", wire.Anthropic, wire.Usage{Input: 30, Output: 5, Total: 35})
	recordUsage(t, metrics, "text-embedding-3-small", wire.Embeddings, wire.Usage{Input: 8, Total: 8})
	recordUsage(t, metrics, "system-one", wire.SystemOne, wire.Usage{Input: 4, Total: 4})

	snapshot := metrics.Snapshot(1)
	window := snapshot.Usage.Window
	if got, want := window.Tokens.Total, uint64(227); got != want {
		t.Fatalf("window total tokens = %d, want %d", got, want)
	}
	// The three message protocols share the chat family, so a client switching
	// between them does not look like three kinds of traffic.
	if got, want := window.Protocols["chat"].Total, uint64(215); got != want {
		t.Errorf("chat tokens = %d, want %d", got, want)
	}
	if got, want := window.Protocols["embeddings"].Total, uint64(8); got != want {
		t.Errorf("embedding tokens = %d, want %d", got, want)
	}
	if got, want := window.Protocols["decision"].Total, uint64(4); got != want {
		t.Errorf("decision tokens = %d, want %d", got, want)
	}
	if got, want := snapshot.Usage.Lifetime.Protocols["embeddings"].Total, uint64(8); got != want {
		t.Errorf("lifetime embedding tokens = %d, want %d", got, want)
	}

	series := snapshot.Series[len(snapshot.Series)-1]
	if series.ChatTokens != 215 || series.EmbeddingTokens != 8 || series.DecisionTokens != 4 {
		t.Errorf("trend split = chat %d / embeddings %d / decision %d, want 215 / 8 / 4",
			series.ChatTokens, series.EmbeddingTokens, series.DecisionTokens)
	}
	if total := series.ChatTokens + series.EmbeddingTokens + series.DecisionTokens; total != series.TotalTokens {
		t.Errorf("trend split sums to %d, want the %d reported total", total, series.TotalTokens)
	}
}

// TestUsageSplitSurvivesRestart checks that the protocol split is persisted
// with the minute buckets and the lifetime totals, so the Operations desk keeps
// its chart across a restart.
func TestUsageSplitSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	metrics, err := Monitor(store)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	recordUsage(t, metrics, "text-embedding-3-small", wire.Embeddings, wire.Usage{Input: 40, Total: 40})
	recordUsage(t, metrics, "gpt-5", wire.Chat, wire.Usage{Input: 10, Output: 2, Total: 12})
	metrics.Close()
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	restored, err := Monitor(reopened)
	if err != nil {
		t.Fatalf("restore monitor: %v", err)
	}
	snapshot := restored.Snapshot(1)
	if got, want := snapshot.Usage.Lifetime.Protocols["embeddings"].Total, uint64(40); got != want {
		t.Errorf("restored lifetime embedding tokens = %d, want %d", got, want)
	}
	if got, want := snapshot.Usage.Window.Protocols["chat"].Total, uint64(12); got != want {
		t.Errorf("restored window chat tokens = %d, want %d", got, want)
	}
	series := snapshot.Series[len(snapshot.Series)-1]
	if series.EmbeddingTokens != 40 || series.ChatTokens != 12 {
		t.Errorf("restored trend split = chat %d / embeddings %d, want 12 / 40", series.ChatTokens, series.EmbeddingTokens)
	}
}

// TestStoreMigratesLegacyDatabase checks that a database written before the
// protocol split existed still opens: the added columns default to "no split"
// instead of failing the schema step and losing all stored metrics.
func TestStoreMigratesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	statements := []string{
		`CREATE TABLE metric_minutes (
			minute INTEGER PRIMARY KEY,
			total INTEGER NOT NULL, success INTEGER NOT NULL, errors INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL, histogram TEXT NOT NULL,
			endpoints TEXT NOT NULL, models TEXT NOT NULL, tiers TEXT NOT NULL, statuses TEXT NOT NULL,
			usage_requests INTEGER NOT NULL, usage_reported INTEGER NOT NULL,
			tokens_input INTEGER NOT NULL, tokens_output INTEGER NOT NULL, tokens_cached INTEGER NOT NULL,
			tokens_reasoning INTEGER NOT NULL, tokens_total INTEGER NOT NULL,
			usage_models TEXT NOT NULL, usage_tiers TEXT NOT NULL, usage_sessions TEXT NOT NULL, usage_keys TEXT NOT NULL
		)`,
		`CREATE TABLE upstream_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time INTEGER NOT NULL, request_id TEXT NOT NULL, session TEXT NOT NULL,
			server_key_id TEXT NOT NULL, model TEXT NOT NULL, tier TEXT NOT NULL, key_id TEXT NOT NULL,
			channel TEXT NOT NULL, anonymous INTEGER NOT NULL, proxy TEXT NOT NULL,
			attempts INTEGER NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
			success INTEGER NOT NULL, outcome TEXT NOT NULL
		)`,
		`INSERT INTO metric_minutes VALUES (
			` + minuteOf(time.Now()) + `, 1, 1, 0, 5, '[0,0,1,0,0,0,0,0,0,0,0]',
			'{}', '{}', '{}', '{}', 1, 1, 10, 0, 0, 0, 10, '{}', '{}', '{}', '{}')`,
		`INSERT INTO upstream_requests (
			time, request_id, session, server_key_id, model, tier, key_id, channel, anonymous, proxy,
			attempts, status, duration_ms, success, outcome)
			VALUES (` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `, 'req_legacy', '', '', 'gpt-5', 'go', 'k1', 'key', 0, 'node',
			1, 200, 5, 1, 'ok')`,
	}
	for _, statement := range statements {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore on legacy database: %v", err)
	}
	defer store.Close()
	metrics, err := Monitor(store)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	snapshot := metrics.Snapshot(1)
	if got, want := snapshot.Usage.Window.Tokens.Input, uint64(10); got != want {
		t.Errorf("restored input tokens = %d, want %d", got, want)
	}
	if len(snapshot.Usage.Window.Protocols) != 0 {
		t.Errorf("legacy rows report no protocol split, got %v", snapshot.Usage.Window.Protocols)
	}
	if len(snapshot.Upstream.Requests) != 1 {
		t.Fatalf("restored requests = %d, want 1", len(snapshot.Upstream.Requests))
	}
	if got := snapshot.Upstream.Requests[0].Protocol; got != "" {
		t.Errorf("legacy request protocol = %q, want empty", got)
	}
}

// minuteOf returns the unix minute of moment, the metric_minutes primary key.
func minuteOf(moment time.Time) string {
	return strconv.FormatInt(moment.Unix()/60, 10)
}

// TestLedgerKeepsProtocol checks that the request ledger names the protocol
// that served a request, so an embedding call is recognizable next to a chat
// call with the same model family.
func TestLedgerKeepsProtocol(t *testing.T) {
	metrics, err := Monitor(nil)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	recordUsage(t, metrics, "text-embedding-3-small", wire.Embeddings, wire.Usage{Input: 3, Total: 3})
	snapshot := metrics.Snapshot(1)
	if len(snapshot.Upstream.Requests) != 1 {
		t.Fatalf("ledger requests = %d, want 1", len(snapshot.Upstream.Requests))
	}
	if got, want := snapshot.Upstream.Requests[0].Protocol, string(wire.Embeddings); got != want {
		t.Errorf("ledger protocol = %q, want %q", got, want)
	}
}
