package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opencode2api/internal/config"
	"opencode2api/internal/protocol"
)

// embeddingUpstream is a stand-in for the OpenCode Go embedding endpoint. It
// records what the gateway sent and answers with the body a test configured.
type embeddingUpstream struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []capturedEmbeddingRequest
	respond  func(w http.ResponseWriter, body map[string]any)
}

type capturedEmbeddingRequest struct {
	Path          string
	Authorization string
	ContentType   string
	Accept        string
	SessionHeader string
	ProjectHeader string
	Body          map[string]any
}

const (
	embeddingsLocalKey  = "local-caller-key"
	embeddingsGoKey     = "go-plan-secret-key"
	embeddingsGoBase    = "/inference/go/openai/v1"
	embeddingsModelFast = "text-embedding-3-small"
)

func newEmbeddingUpstream(t *testing.T, respond func(w http.ResponseWriter, body map[string]any)) *embeddingUpstream {
	t.Helper()
	upstream := &embeddingUpstream{respond: respond}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		upstream.mu.Lock()
		upstream.requests = append(upstream.requests, capturedEmbeddingRequest{
			Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), ContentType: r.Header.Get("Content-Type"),
			Accept: r.Header.Get("Accept"), SessionHeader: r.Header.Get("x-opencode-session"),
			ProjectHeader: r.Header.Get("x-opencode-project"), Body: body,
		})
		upstream.mu.Unlock()
		upstream.respond(w, body)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *embeddingUpstream) captured() []capturedEmbeddingRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]capturedEmbeddingRequest(nil), u.requests...)
}

// defaultEmbeddingResponse returns one float vector per input, in order, like
// the real endpoint.
func defaultEmbeddingResponse(w http.ResponseWriter, body map[string]any) {
	inputs, ok := body["input"].([]any)
	if !ok {
		inputs = []any{body["input"]}
	}
	data := make([]map[string]any, 0, len(inputs))
	for index, input := range inputs {
		// The vector encodes the input length so order and content survive a
		// round trip visibly.
		length := float64(len(imgString(input)))
		data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": []float64{length, 0.5, -0.25}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list", "data": data, "model": body["model"],
		"usage": map[string]any{"prompt_tokens": len(inputs), "total_tokens": len(inputs)},
	})
}

func imgString(value any) string {
	text, _ := value.(string)
	return text
}

func newEmbeddingsTestGateway(t *testing.T, baseURL string, goKeys []string, protocols map[string]string) *Gateway {
	t.Helper()
	cfg := config.Config{
		Listen:     "127.0.0.1:0",
		ServerKeys: []string{embeddingsLocalKey},
		GoKeys:     goKeys,
		Proxies:    []string{"direct"},
		Upstream:   config.UpstreamConfig{GoEmbeddings: baseURL},
		Retry:      config.RetryConfig{MaxAttempts: 2, TimeoutSeconds: 10},
		Models:     config.ModelsConfig{RefreshSeconds: 300, Protocols: protocols},
		Performance: config.PerformanceConfig{
			MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeoutSeconds: 30,
			ConnectTimeoutSeconds: 2, FailureCooldownSeconds: 5,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g, err := New(cfg, logger, nil, nil)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	return g
}

func postEmbeddings(t *testing.T, handler http.Handler, key string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestEmbeddingsForwardsRequestVerbatim(t *testing.T) {
	upstream := newEmbeddingUpstream(t, defaultEmbeddingResponse)
	gateway := newEmbeddingsTestGateway(t, upstream.server.URL+embeddingsGoBase, []string{embeddingsGoKey}, nil)
	handler := gateway.Handler()

	body := `{"model":"` + embeddingsModelFast + `","input":["first","second input"],"encoding_format":"float","dimensions":256,"user":"caller-7","future_field":{"nested":true}}`
	recorder := postEmbeddings(t, handler, embeddingsLocalKey, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	captured := upstream.captured()
	if len(captured) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(captured))
	}
	got := captured[0]
	if got.Path != embeddingsGoBase+"/embeddings" {
		t.Fatalf("upstream path = %q, want %q", got.Path, embeddingsGoBase+"/embeddings")
	}
	if got.Authorization != "Bearer "+embeddingsGoKey {
		t.Fatalf("upstream authorization = %q, want the Go key", got.Authorization)
	}
	if got.SessionHeader != "" || got.ProjectHeader != "" {
		t.Fatalf("upstream received session headers: session=%q project=%q", got.SessionHeader, got.ProjectHeader)
	}
	// Optional and unknown fields survive because the body is forwarded as-is.
	for field, want := range map[string]any{"model": embeddingsModelFast, "encoding_format": "float", "dimensions": float64(256), "user": "caller-7"} {
		if got.Body[field] != want {
			t.Fatalf("forwarded %s = %#v, want %#v", field, got.Body[field], want)
		}
	}
	if _, ok := got.Body["future_field"]; !ok {
		t.Fatalf("unknown request field was dropped: %#v", got.Body)
	}
	// The response is relayed unchanged, order included.
	var response struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
		Usage  map[string]any   `json:"usage"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Object != "list" || len(response.Data) != 2 {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	for index, item := range response.Data {
		if item["index"] != float64(index) {
			t.Fatalf("data[%d].index = %v, want %d", index, item["index"], index)
		}
	}
	if response.Usage["prompt_tokens"] != float64(2) {
		t.Fatalf("usage.prompt_tokens = %v, want 2", response.Usage["prompt_tokens"])
	}
}

func TestEmbeddingsModelPassthroughAndBase64(t *testing.T) {
	upstream := newEmbeddingUpstream(t, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"object": "embedding", "index": 0, "embedding": "3q2+7w=="}},
			"model":  body["model"],
			"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1, "cost": 0.0001},
		})
	})
	gateway := newEmbeddingsTestGateway(t, upstream.server.URL+embeddingsGoBase, []string{embeddingsGoKey}, nil)
	handler := gateway.Handler()

	for _, model := range []string{"text-embedding-3-small", "qwen3-embedding-8b", "gemini-embedding-2"} {
		recorder := postEmbeddings(t, handler, embeddingsLocalKey, `{"model":"`+model+`","input":"hello"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", model, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"embedding":"3q2+7w=="`) {
			t.Fatalf("%s: base64 vector was rewritten: %s", model, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"cost":0.0001`) {
			t.Fatalf("%s: unknown usage field was dropped: %s", model, recorder.Body.String())
		}
	}
	captured := upstream.captured()
	if len(captured) != 3 {
		t.Fatalf("upstream requests = %d, want 3", len(captured))
	}
	for index, model := range []string{"text-embedding-3-small", "qwen3-embedding-8b", "gemini-embedding-2"} {
		if captured[index].Body["model"] != model {
			t.Fatalf("request %d forwarded model %#v, want %s", index, captured[index].Body["model"], model)
		}
	}
}

func TestEmbeddingsLocalAuthAndUpstreamErrors(t *testing.T) {
	upstream := newEmbeddingUpstream(t, func(w http.ResponseWriter, body map[string]any) {
		status := http.StatusOK
		if value, ok := body["input"].(string); ok {
			switch value {
			case "bad-request":
				status = http.StatusBadRequest
			case "unauthorized":
				status = http.StatusUnauthorized
			case "forbidden":
				status = http.StatusForbidden
			case "rate-limited":
				status = http.StatusTooManyRequests
			case "server-error":
				status = http.StatusInternalServerError
			}
		}
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream says no","type":"invalid_request_error"}}`)
			return
		}
		defaultEmbeddingResponse(w, body)
	})
	gateway := newEmbeddingsTestGateway(t, upstream.server.URL+embeddingsGoBase, []string{embeddingsGoKey}, nil)
	handler := gateway.Handler()

	wrong := postEmbeddings(t, handler, "not-the-local-key", `{"model":"m","input":"x"}`)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong local key status = %d, want 401", wrong.Code)
	}
	if len(upstream.captured()) != 0 {
		t.Fatalf("an unauthenticated request reached the upstream")
	}

	for input, want := range map[string]int{
		"bad-request": http.StatusBadRequest, "unauthorized": http.StatusUnauthorized,
		"forbidden": http.StatusForbidden, "rate-limited": http.StatusTooManyRequests,
		"server-error": http.StatusInternalServerError,
	} {
		recorder := postEmbeddings(t, handler, embeddingsLocalKey, `{"model":"m","input":"`+input+`"}`)
		if recorder.Code != want {
			t.Fatalf("%s: status = %d, want %d", input, recorder.Code, want)
		}
		if strings.Contains(recorder.Body.String(), embeddingsGoKey) {
			t.Fatalf("%s: response leaked the upstream key", input)
		}
	}
	if !strings.Contains(postEmbeddings(t, handler, embeddingsLocalKey, `{"model":"m","input":"forbidden"}`).Body.String(), "upstream says no") {
		t.Fatalf("upstream error message was not preserved")
	}
}

func TestEmbeddingsRejectsWhatItCannotServe(t *testing.T) {
	upstream := newEmbeddingUpstream(t, defaultEmbeddingResponse)
	gateway := newEmbeddingsTestGateway(t, upstream.server.URL+embeddingsGoBase, []string{embeddingsGoKey},
		map[string]string{"text-embedding-3-small": "embeddings", "gpt-chat": "chat"})
	gateway.catalog.Replace(nil, []string{"text-embedding-3-small", "gpt-chat"})
	handler := gateway.Handler()

	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing model", `{"input":"x"}`, http.StatusBadRequest},
		{"missing input", `{"model":"text-embedding-3-small"}`, http.StatusBadRequest},
		{"streaming", `{"model":"text-embedding-3-small","input":"x","stream":true}`, http.StatusBadRequest},
		{"chat model", `{"model":"gpt-chat","input":"x"}`, http.StatusBadRequest},
		{"unknown model is forwarded", `{"model":"vendor-new-embedding","input":"x"}`, http.StatusOK},
		{"embedding model", `{"model":"text-embedding-3-small","input":"x"}`, http.StatusOK},
	}
	for _, testCase := range cases {
		recorder := postEmbeddings(t, handler, embeddingsLocalKey, testCase.body)
		if recorder.Code != testCase.want {
			t.Fatalf("%s: status = %d, want %d (body %s)", testCase.name, recorder.Code, testCase.want, recorder.Body.String())
		}
	}

	// A chat endpoint must not accept a model that only speaks embeddings.
	chatRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"text-embedding-3-small","messages":[{"role":"user","content":"hi"}]}`))
	chatRequest.Header.Set("Authorization", "Bearer "+embeddingsLocalKey)
	chatRecorder := httptest.NewRecorder()
	handler.ServeHTTP(chatRecorder, chatRequest)
	if chatRecorder.Code != http.StatusBadRequest || !strings.Contains(chatRecorder.Body.String(), "/v1/embeddings") {
		t.Fatalf("chat endpoint accepted an embedding model: %d %s", chatRecorder.Code, chatRecorder.Body.String())
	}

	// Discovery labels the model as an embedding model on its own endpoint.
	modelsRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRequest.Header.Set("Authorization", "Bearer "+embeddingsLocalKey)
	modelsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(modelsRecorder, modelsRequest)
	var listing struct {
		Data []struct {
			ID                 string   `json:"id"`
			NativeProtocol     string   `json:"native_protocol"`
			SupportedEndpoints []string `json:"supported_endpoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(modelsRecorder.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	found := false
	for _, entry := range listing.Data {
		if entry.ID != "text-embedding-3-small" {
			if entry.NativeProtocol == string(protocol.Embeddings) {
				t.Fatalf("unexpected embedding entry %q", entry.ID)
			}
			continue
		}
		found = true
		if entry.NativeProtocol != string(protocol.Embeddings) {
			t.Fatalf("native_protocol = %q, want embeddings", entry.NativeProtocol)
		}
		if len(entry.SupportedEndpoints) != 1 || entry.SupportedEndpoints[0] != "/v1/embeddings" {
			t.Fatalf("supported_endpoints = %v, want [/v1/embeddings]", entry.SupportedEndpoints)
		}
	}
	if !found {
		t.Fatalf("embedding model missing from /v1/models: %s", modelsRecorder.Body.String())
	}
}

func TestEmbeddingsRequireGoKeys(t *testing.T) {
	upstream := newEmbeddingUpstream(t, defaultEmbeddingResponse)
	gateway := newEmbeddingsTestGateway(t, upstream.server.URL+embeddingsGoBase, nil, nil)
	recorder := postEmbeddings(t, gateway.Handler(), embeddingsLocalKey, `{"model":"text-embedding-3-small","input":"x"}`)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", recorder.Code, recorder.Body.String())
	}
	if len(upstream.captured()) != 0 {
		t.Fatalf("a request without a Go key reached the upstream")
	}
}

func TestEmbeddingsEndpointJoin(t *testing.T) {
	cases := map[string]string{
		"https://opencode.ai/inference/go/openai/v1":  "https://opencode.ai/inference/go/openai/v1/embeddings",
		"https://opencode.ai/inference/go/openai/v1/": "https://opencode.ai/inference/go/openai/v1/embeddings",
		"http://127.0.0.1:1234":                       "http://127.0.0.1:1234/v1/embeddings",
	}
	for base, want := range cases {
		if got := embeddingsEndpoint(base); got != want {
			t.Fatalf("embeddingsEndpoint(%q) = %q, want %q", base, got, want)
		}
	}
}
