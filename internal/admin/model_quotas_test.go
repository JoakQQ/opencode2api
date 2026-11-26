package admin

import (
	"testing"

	"opencode2api/internal/config"
	modelcatalog "opencode2api/internal/models"
	wire "opencode2api/internal/protocol"
)

// TestModelTags checks the badge a catalog row carries on the models and
// quotas page: message protocols collapse into "chat", System One is
// "decision" and the embeddings endpoint is "embeddings".
func TestModelTags(t *testing.T) {
	cases := []struct {
		name  string
		model modelcatalog.RouteDiagnostic
		want  []string
	}{
		{
			name: "chat model on go",
			model: modelcatalog.RouteDiagnostic{
				NativeProtocol:  wire.Chat,
				NativeProtocols: map[config.Tier]wire.Protocol{config.TierGo: wire.Chat},
			},
			want: []string{"chat"},
		},
		{
			name: "embedding model on go",
			model: modelcatalog.RouteDiagnostic{
				NativeProtocol:  wire.Embeddings,
				NativeProtocols: map[config.Tier]wire.Protocol{config.TierGo: wire.Embeddings},
			},
			want: []string{"embeddings"},
		},
		{
			name: "decision model on zen",
			model: modelcatalog.RouteDiagnostic{
				NativeProtocol:  wire.SystemOne,
				NativeProtocols: map[config.Tier]wire.Protocol{config.TierZen: wire.SystemOne},
			},
			want: []string{"decision"},
		},
		{
			name: "message protocols share the chat tag",
			model: modelcatalog.RouteDiagnostic{
				NativeProtocol: wire.Anthropic,
				NativeProtocols: map[config.Tier]wire.Protocol{
					config.TierZen: wire.Anthropic, config.TierGo: wire.Responses,
				},
			},
			want: []string{"chat"},
		},
		{
			name: "a model serving two families gets both tags",
			model: modelcatalog.RouteDiagnostic{
				NativeProtocol: wire.Chat,
				NativeProtocols: map[config.Tier]wire.Protocol{
					config.TierZen: wire.Chat, config.TierGo: wire.Embeddings,
				},
			},
			want: []string{"chat", "embeddings"},
		},
		{
			name:  "no protocol reports no tag",
			model: modelcatalog.RouteDiagnostic{},
			want:  []string{},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := modelTags(test.model)
			if len(got) != len(test.want) {
				t.Fatalf("modelTags = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("modelTags = %v, want %v", got, test.want)
				}
			}
		})
	}
}
