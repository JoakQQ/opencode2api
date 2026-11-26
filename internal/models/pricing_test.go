package models

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newPricingStore builds a registry with the given metadata cache entries, so a
// test controls which models the metadata endpoint knows.
func newPricingStore(t *testing.T, models map[string]any) *PricingRegistry {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if models != nil {
		encoded, err := json.Marshal(map[string]any{"updated_at": time.Now().UTC(), "models": models})
		if err != nil {
			t.Fatalf("encode metadata cache: %v", err)
		}
		if err := os.WriteFile(configPath+".models.dev.json", encoded, 0o600); err != nil {
			t.Fatalf("write metadata cache: %v", err)
		}
	}
	return PricingStore(configPath, logger)
}

func float(value float64) *float64 { return &value }

// TestListingPricesFillModelsTheMetadataOmits checks the reason the routing page
// used to report metadata_model_missing for the decision and embedding models:
// the metadata endpoint does not list them, so their own plan's listing is the
// only price source.
func TestListingPricesFillModelsTheMetadataOmits(t *testing.T) {
	store := newPricingStore(t, nil)
	store.mu.Lock()
	store.updatedAt = time.Now().UTC()
	store.models["known-chat"] = Price{ID: "known-chat", Input: float(0.15), Output: float(0.6)}
	store.mu.Unlock()

	if decision := store.Decide("gemini-embedding-2"); decision.Source != "metadata_model_missing" || decision.Allowed {
		t.Fatalf("before the listing, decision = %+v, want metadata_model_missing", decision)
	}

	store.ObserveListing([]ModelEntry{
		{ID: "gemini-embedding-2", Type: "embedding", ContextWindow: 8192, MaxOutput: 3072, Cost: &Cost{Input: float(0.2), Output: float(0)}},
		{ID: "gpt-6-luna-decision", Type: "decision", Cost: &Cost{Input: float(0.1), Output: float(0)}},
		{ID: "zero-priced-model", Type: "chat", Cost: &Cost{Input: float(0), Output: float(0)}},
		{ID: "no-price", Type: "chat"},
	})

	paid := store.Decide("gemini-embedding-2")
	if paid.Allowed || paid.Source != "listing_paid" || !paid.Known {
		t.Fatalf("embedding decision = %+v, want listing_paid", paid)
	}
	if price, ok := store.Price("gemini-embedding-2"); !ok || price.ContextWindow != 8192 || price.MaxOutput != 3072 {
		t.Fatalf("embedding price = %+v (ok=%v), want the listing's limits", price, ok)
	}
	if free := store.Decide("zero-priced-model"); !free.Allowed || free.Source != "listing_free" {
		t.Fatalf("free decision = %+v, want listing_free", free)
	}
	if missing := store.Decide("no-price"); missing.Known || missing.Source != "metadata_model_missing" {
		t.Fatalf("unpriced decision = %+v, want it still unknown", missing)
	}
	if known := store.Decide("known-chat"); known.Source != "metadata_paid" {
		t.Fatalf("metadata price was overridden: %+v", known)
	}
}

// TestListingPricesSurviveMetadataRefresh checks the listing prices live apart
// from the metadata snapshot: a refresh replaces the metadata and must not drop
// the models it does not list.
func TestListingPricesSurviveMetadataRefresh(t *testing.T) {
	store := newPricingStore(t, nil)
	store.ObserveListing([]ModelEntry{{ID: "text-embedding-3-small", Type: "embedding", Cost: &Cost{Input: float(0.02), Output: float(0)}}})

	store.mu.Lock()
	store.models = map[string]Price{"known-chat": {ID: "known-chat", Input: float(1), Output: float(1)}}
	store.updatedAt = time.Now().UTC()
	store.mu.Unlock()

	if _, ok := store.Price("text-embedding-3-small"); !ok {
		t.Fatal("a metadata refresh dropped an observed listing price")
	}
}

// TestListingPricesReportReadiness checks a plan listing alone makes the registry
// ready and counts its models, so the console and the anonymous filter stop
// waiting for metadata that will never describe them.
func TestListingPricesReportReadiness(t *testing.T) {
	store := newPricingStore(t, nil)
	if snapshot := store.Snapshot(); snapshot.Ready || snapshot.Models != 0 {
		t.Fatalf("empty registry snapshot = %+v, want not ready", snapshot)
	}
	store.ObserveListing([]ModelEntry{
		{ID: "gemini-decision-1", Type: "decision", Cost: &Cost{Input: float(0.1), Output: float(0)}},
		{ID: "qwen3-embedding-8b", Type: "embedding", Cost: &Cost{Input: float(0.01), Output: float(0)}},
	})
	snapshot := store.Snapshot()
	if !snapshot.Ready {
		t.Fatal("a listing with prices must make the registry ready")
	}
	if snapshot.Models != 2 {
		t.Fatalf("snapshot models = %d, want 2", snapshot.Models)
	}
}

// TestListingPriceParsesCostEnvelope checks the /v1/models cost envelope, whose
// rates sit under "text", is read into the registry's price shape.
func TestListingPriceParsesCostEnvelope(t *testing.T) {
	raw := `{"id":"m","type":"chat","limit":{"context":1000,"input":900,"output":100},
		"cost":{"text":{"input":{"price":0.15,"cache":{"read":0.003,"write":0.02}},"output":{"price":0.6}}}}`
	var payload modelsResponse
	if err := json.Unmarshal([]byte(`{"data":[`+raw+`]}`), &payload); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	row := payload.Data[0]
	entry := listingEntry(row.ID, row.Type, row.Limit, row.Cost)
	price, ok := entry.listingPrice()
	if !ok {
		t.Fatal("listing price was not parsed")
	}
	if price.Input == nil || *price.Input != 0.15 || price.Output == nil || *price.Output != 0.6 {
		t.Fatalf("price = %+v, want 0.15 / 0.6", price)
	}
	if price.CacheRead == nil || *price.CacheRead != 0.003 || price.CacheWrite == nil || *price.CacheWrite != 0.02 {
		t.Fatalf("cache rates = %+v, want 0.003 / 0.02", price)
	}
	if price.ContextWindow != 1000 || price.MaxInput != 900 || price.MaxOutput != 100 {
		t.Fatalf("limits = %d/%d/%d, want 1000/900/100", price.ContextWindow, price.MaxInput, price.MaxOutput)
	}
}

// TestListingPriceReadsCacheWriteLifetimeMap checks the cache-write rate is read
// from the lifetime-to-price map the listing uses for some models, instead of
// failing the whole listing with a decode error.
func TestListingPriceReadsCacheWriteLifetimeMap(t *testing.T) {
	raw := `{"id":"gpt-6-luna","type":"chat","cost":{"text":{"input":{"price":1,"cache":{"read":0.1,"write":{"30m":0.125,"12h":0.25}}},"output":{"price":2}}}}`
	var payload modelsResponse
	if err := json.Unmarshal([]byte(`{"data":[`+raw+`]}`), &payload); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	row := payload.Data[0]
	price, ok := listingEntry(row.ID, row.Type, row.Limit, row.Cost).listingPrice()
	if !ok {
		t.Fatal("listing price was not parsed")
	}
	if price.CacheWrite == nil || *price.CacheWrite != 0.125 {
		t.Fatalf("cache write = %v, want the shortest-lived 0.125", price.CacheWrite)
	}
	if price.CacheRead == nil || *price.CacheRead != 0.1 {
		t.Fatalf("cache read = %v, want 0.1", price.CacheRead)
	}
}
