package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opencode2api/internal/config"
	modelcatalog "opencode2api/internal/models"
)

// newCatalogTestGateway builds a gateway whose catalog can be filled by hand.
// credentials decides which keys exist, so a test can describe a plan.
func newCatalogTestGateway(t *testing.T, credentials config.Config) *Gateway {
	t.Helper()
	cfg := config.Config{
		Listen:     "127.0.0.1:0",
		ServerKeys: []string{"local-key"},
		ZenKeys:    credentials.ZenKeys,
		GoKeys:     credentials.GoKeys,
		Anonymous:  credentials.Anonymous,
		Proxies:    []string{"direct"},
		Upstream:   config.UpstreamConfig{Zen: "http://127.0.0.1:1/zen", Go: "http://127.0.0.1:1/zen/go"},
		Retry:      config.RetryConfig{MaxAttempts: 1, TimeoutSeconds: 5},
		Models:     config.ModelsConfig{RefreshSeconds: 300},
		Performance: config.PerformanceConfig{
			MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeoutSeconds: 30,
			ConnectTimeoutSeconds: 1, FailureCooldownSeconds: 5,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g, err := New(cfg, logger, nil, nil)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	return g
}

// TestRefreshZenClearsTierWithoutCredential checks that a gateway with no Zen
// key and no anonymous lane empties the Zen tier instead of keeping models a
// previous configuration discovered, which would advertise models that cannot
// be routed.
func TestRefreshZenClearsTierWithoutCredential(t *testing.T) {
	g := newCatalogTestGateway(t, config.Config{GoKeys: []string{"go-key"}})
	g.catalog.ReplaceWithCapabilities([]modelcatalog.ModelEntry{{ID: "left-over-zen", Type: "chat"}}, nil, nil, nil, nil)
	if _, fetched := g.catalog.Advertised("left-over-zen", config.TierZen); !fetched {
		t.Fatal("setup: the zen tier should start populated")
	}

	entries, anonymous := g.refreshZen(context.Background())
	if entries == nil {
		t.Fatal("refreshZen returned nil; an empty listing must clear the tier")
	}
	if len(entries) != 0 {
		t.Fatalf("refreshZen = %v, want an empty listing", entries)
	}
	if anonymous {
		t.Error("an empty listing must not be reported as an anonymous-lane result")
	}
	g.catalog.ReplaceWithCapabilities(entries, nil, nil, nil, nil)
	if listed, _ := g.catalog.Advertised("left-over-zen", config.TierZen); listed {
		t.Error("the zen tier still advertises a model after it was cleared")
	}
}

// TestRefreshGoClearsTierWithoutKey checks the Go tier is emptied when no Go key
// remains, so models a removed subscription used to list stop being advertised.
func TestRefreshGoClearsTierWithoutKey(t *testing.T) {
	g := newCatalogTestGateway(t, config.Config{ZenKeys: []string{"zen-key"}})
	g.catalog.ReplaceWithCapabilities(nil, []modelcatalog.ModelEntry{{ID: "left-over-go", Type: "chat"}}, nil, nil, nil)
	if _, fetched := g.catalog.Advertised("left-over-go", config.TierGo); !fetched {
		t.Fatal("setup: the go tier should start populated")
	}

	entries := g.refreshGo(context.Background())
	if entries == nil {
		t.Fatal("refreshGo returned nil; an empty listing must clear the tier")
	}
	if len(entries) != 0 {
		t.Fatalf("refreshGo = %v, want an empty listing", entries)
	}
	g.catalog.ReplaceWithCapabilities(nil, entries, nil, nil, nil)
	if listed, _ := g.catalog.Advertised("left-over-go", config.TierGo); listed {
		t.Error("the go tier still advertises a model after it was cleared")
	}
}

// TestAnonymousModelsNarrowedToFreeList checks the anonymous lane's public
// listing is narrowed to the free models it can serve: the public listing is
// the whole Zen catalog, and a paid model there never has a route.
func TestAnonymousModelsNarrowedToFreeList(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	zero, paid := 0.0, 3.5
	cache := map[string]any{
		"updated_at": time.Now().UTC(),
		"models": map[string]any{
			"hy4-flash-free": map[string]any{"id": "hy4-flash-free", "input_cost": zero, "output_cost": zero},
			"paid-model":     map[string]any{"id": "paid-model", "input_cost": paid, "output_cost": paid},
		},
	}
	encoded, err := json.Marshal(cache)
	if err != nil {
		t.Fatalf("encode metadata cache: %v", err)
	}
	if err := os.WriteFile(configPath+".models.dev.json", encoded, 0o600); err != nil {
		t.Fatalf("write metadata cache: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := modelcatalog.PricingStore(configPath, logger)
	g := newCatalogTestGateway(t, config.Config{GoKeys: []string{"go-key"}, Anonymous: true})
	g.catalog.SetPricingStore(store)
	if !g.catalog.MetadataSnapshot().Ready {
		t.Fatal("setup: price metadata should be ready from the cache")
	}

	entries := []modelcatalog.ModelEntry{
		{ID: "hy4-flash-free", Type: "chat"},
		{ID: "paid-model", Type: "chat"},
		{ID: "unpriced-model", Type: "chat"},
	}
	filtered := g.anonymousModels(entries)
	if len(filtered) != 1 || filtered[0].ID != "hy4-flash-free" {
		t.Fatalf("anonymousModels = %v, want only the free model", filtered)
	}
}

// TestAnonymousModelsUseListingPrices checks the filter judges a model the
// metadata endpoint omits on the price the plan's own listing publishes, which
// is the only price source for the decision and embedding models.
func TestAnonymousModelsUseListingPrices(t *testing.T) {
	price := func(value float64) *float64 { return &value }
	g := newCatalogTestGateway(t, config.Config{GoKeys: []string{"go-key"}, Anonymous: true})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g.catalog.SetPricingStore(modelcatalog.PricingStore(filepath.Join(t.TempDir(), "config.json"), logger))
	entries := []modelcatalog.ModelEntry{
		{ID: "qwen3-embedding-8b", Type: "embedding", Cost: &modelcatalog.Cost{Input: price(0.01), Output: price(0)}},
		{ID: "zero-priced-model", Type: "chat", Cost: &modelcatalog.Cost{Input: price(0), Output: price(0)}},
		{ID: "unpriced-model", Type: "chat"},
	}
	g.catalog.ObserveListingPrices(entries, nil)
	if !g.catalog.MetadataSnapshot().Ready {
		t.Fatal("a listing price must make the pricing registry ready")
	}
	filtered := g.anonymousModels(entries)
	if len(filtered) != 1 || filtered[0].ID != "zero-priced-model" {
		t.Fatalf("anonymousModels = %v, want only the free model", filtered)
	}
}

// TestAnonymousModelsKeptWhileMetadataPending checks the filter is skipped
// before the price metadata arrives: the rule would then be the name rule
// alone and would hide priced-free models until the next refresh.
func TestAnonymousModelsKeptWhileMetadataPending(t *testing.T) {
	g := newCatalogTestGateway(t, config.Config{GoKeys: []string{"go-key"}, Anonymous: true})
	if g.catalog.MetadataSnapshot().Ready {
		t.Fatal("setup: a gateway without metadata must not report ready")
	}
	entries := []modelcatalog.ModelEntry{{ID: "priced-free-model", Type: "chat"}}
	if filtered := g.anonymousModels(entries); len(filtered) != len(entries) {
		t.Fatalf("anonymousModels = %v, want the listing unchanged while metadata is pending", filtered)
	}
}
