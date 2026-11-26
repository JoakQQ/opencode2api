package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
)

// listingServer serves one /v1/models answer with the given rows.
func listingServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestFetchModelsKeepsDeclaredKind checks that the kind each listing row
// declares survives discovery: it is the only source that names a
// subscription's decision and embedding models.
func TestFetchModelsKeepsDeclaredKind(t *testing.T) {
	server := listingServer(t, `{"object":"list","data":[
		{"id":"gpt-5","object":"model","type":"chat"},
		{"id":"gpt-6-luna-decision","object":"model","type":"decision"},
		{"id":"text-embedding-3-small","object":"model","type":"embedding"},
		{"id":"gpt-image-2.5-sunburst","object":"model","type":"image"},
		{"id":"untyped-model","object":"model"}
	]}`)
	entries, status, err := FetchModels(context.Background(), server.Client(), server.URL, "key")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(entries))
	}
	kinds := map[string]string{}
	for _, entry := range entries {
		kinds[entry.ID] = entry.Type
	}
	for id, want := range map[string]string{
		"gpt-5": "chat", "gpt-6-luna-decision": "decision", "text-embedding-3-small": "embedding",
		"gpt-image-2.5-sunburst": "image", "untyped-model": "",
	} {
		if got := kinds[id]; got != want {
			t.Errorf("type of %q = %q, want %q", id, got, want)
		}
	}
}

// TestModelEntryProtocol checks which declared kinds map to a protocol the
// gateway can speak.
func TestModelEntryProtocol(t *testing.T) {
	cases := []struct {
		kind  string
		want  wire.Protocol
		valid bool
	}{
		{"chat", wire.Chat, true},
		{"Chat", wire.Chat, true},
		{" decision ", wire.SystemOne, true},
		{"embedding", wire.Embeddings, true},
		{"embeddings", wire.Embeddings, true},
		{"image", "", false},
		{"realtime", "", false},
		{"video", "", false},
		{"", "", false},
	}
	for _, test := range cases {
		protocol, ok := ModelEntry{ID: "m", Type: test.kind}.Protocol()
		if ok != test.valid || protocol != test.want {
			t.Errorf("type %q -> (%q, %v), want (%q, %v)", test.kind, protocol, ok, test.want, test.valid)
		}
	}
}

// TestDeclaredKindsBecomeRoutes checks that the kinds a listing declares make
// the model servable: the capability catalog has no entry for the decision and
// embedding models, so before the merge they were neither listed nor routable.
func TestDeclaredKindsBecomeRoutes(t *testing.T) {
	catalog := Catalog(config.TierGo, nil)
	catalog.ReplaceWithCapabilities(
		[]ModelEntry{{ID: "gpt-5", Type: "chat"}},
		[]ModelEntry{
			{ID: "gpt-5", Type: "chat"},
			{ID: "gpt-6-luna-decision", Type: "decision"},
			{ID: "text-embedding-3-small", Type: "embedding"},
			{ID: "gpt-image-2.5-sunburst", Type: "image"},
		},
		nil, nil, nil,
	)
	listed := map[string]bool{}
	for _, model := range catalog.List() {
		listed[model] = true
	}
	if !listed["gpt-5"] || !listed["gpt-6-luna-decision"] || !listed["text-embedding-3-small"] {
		t.Fatalf("List() = %v, want chat, decision and embedding models", catalog.List())
	}
	if listed["gpt-image-2.5-sunburst"] {
		t.Fatalf("List() = %v, an image model must stay unroutable", catalog.List())
	}
	if !catalog.EmbeddingModel("text-embedding-3-small") {
		t.Error("EmbeddingModel(text-embedding-3-small) = false, want true")
	}
	if catalog.EmbeddingModel("gpt-5") {
		t.Error("EmbeddingModel(gpt-5) = true, want false")
	}

	embeddingRoute, err := catalog.RouteForTier("text-embedding-3-small", config.TierGo, false, true)
	if err != nil {
		t.Fatalf("embedding route: %v", err)
	}
	if embeddingRoute.Protocol != wire.Embeddings {
		t.Errorf("embedding route protocol = %q, want embeddings", embeddingRoute.Protocol)
	}
	decisionRoute, err := catalog.RouteForTier("gpt-6-luna-decision", config.TierGo, false, true)
	if err != nil {
		t.Fatalf("decision route: %v", err)
	}
	if decisionRoute.Protocol != wire.SystemOne {
		t.Errorf("decision route protocol = %q, want systemone", decisionRoute.Protocol)
	}
	if _, err := catalog.RouteForTier("gpt-image-2.5-sunburst", config.TierGo, false, true); err == nil {
		t.Error("an image model must not build a route")
	}

	if diagnostic := catalog.Diagnostic("text-embedding-3-small", wire.Embeddings, false, true, false); diagnostic.NativeProtocol != wire.Embeddings {
		t.Errorf("diagnostic native protocol = %q, want embeddings", diagnostic.NativeProtocol)
	}
}

// TestDeclaredChatKeepsDocumentedProtocol checks that a generic chat
// declaration does not overwrite a protocol the capability catalog already
// knows: OpenCode serves some models through Messages on one tier and Chat on
// another, and only the documented route is correct there.
func TestDeclaredChatKeepsDocumentedProtocol(t *testing.T) {
	catalog := Catalog(config.TierGo, nil)
	catalog.ReplaceWithCapabilities(
		nil,
		[]ModelEntry{{ID: "minimax-m3", Type: "chat"}},
		map[config.Tier]map[string]wire.Protocol{config.TierGo: {"minimax-m3": wire.Anthropic}},
		map[config.Tier]map[string]bool{config.TierGo: {"text-embedding-3-small": true}},
		nil,
	)
	route, err := catalog.RouteForTier("minimax-m3", config.TierGo, false, true)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if route.Protocol != wire.Anthropic {
		t.Errorf("route protocol = %q, want the documented anthropic", route.Protocol)
	}
}

// TestDeclaredKindClearsUnsupported checks that a model the capability catalog
// marked unsupported becomes routable once its tier's listing declares a kind
// the gateway can speak.
func TestDeclaredKindClearsUnsupported(t *testing.T) {
	catalog := Catalog(config.TierGo, nil)
	catalog.ReplaceWithCapabilities(
		nil,
		[]ModelEntry{{ID: "text-embedding-3-small", Type: "embedding"}},
		nil,
		map[config.Tier]map[string]bool{config.TierGo: {"text-embedding-3-small": true}},
		nil,
	)
	if !catalog.Supported("text-embedding-3-small") {
		t.Fatal("a declared embedding model must be supported on its tier")
	}
}

// TestAdvertisedReportsFetched checks the accessor the embeddings endpoint uses
// to tell "the go plan does not include this model" from "the go listing is not
// known yet".
func TestAdvertisedReportsFetched(t *testing.T) {
	catalog := Catalog(config.TierGo, nil)
	if listed, fetched := catalog.Advertised("text-embedding-3-small", config.TierGo); listed || fetched {
		t.Fatalf("empty catalog reported listed=%v fetched=%v, want false/false", listed, fetched)
	}
	catalog.ReplaceWithCapabilities(
		[]ModelEntry{{ID: "voyage-4", Type: "embedding"}},
		[]ModelEntry{{ID: "text-embedding-3-small", Type: "embedding"}},
		nil, nil, nil,
	)
	if listed, fetched := catalog.Advertised("text-embedding-3-small", config.TierGo); !listed || !fetched {
		t.Errorf("go-listed model reported listed=%v fetched=%v, want true/true", listed, fetched)
	}
	if listed, fetched := catalog.Advertised("voyage-4", config.TierGo); listed || !fetched {
		t.Errorf("zen-only model reported listed=%v fetched=%v, want false/true", listed, fetched)
	}
	if listed, fetched := catalog.Advertised("voyage-4", config.TierZen); !listed || !fetched {
		t.Errorf("zen-listed model reported listed=%v fetched=%v, want true/true", listed, fetched)
	}
}
