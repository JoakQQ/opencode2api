# opencode2api: OpenCode Go Embeddings Upstream API

## 1. Objective

Add an OpenAI-compatible embeddings endpoint to `opencode2api` that forwards embedding requests to OpenCode Go. The gateway must act as a transparent authenticated proxy: it must not generate, transform, truncate, reorder, or otherwise reinterpret embedding vectors.

This document is an implementation specification. Inspect the current `opencode2api` repository and preserve its existing language, framework, configuration conventions, authentication middleware, HTTP client, logging, and error-handling patterns before changing code.

## 2. Official upstream contract

OpenCode Go documents the following endpoint:

```text
POST https://opencode.ai/inference/go/openai/v1/embeddings
```

Authentication:

```http
Authorization: Bearer <OPENCODE_GO_API_KEY>
Content-Type: application/json
```

The Go API key is the same key used for the subscriber's OpenCode Go access. Embedding requests do **not** require `x-opencode-session`.

Documented Go embedding model IDs and input prices (per 1M input tokens):

| Model | Model ID | Price |
|---|---|---:|
| text-embedding-3-small | `text-embedding-3-small` | $0.02 |
| Qwen3 Embedding 8B | `qwen3-embedding-8b` | $0.01 |
| Gemini Embedding 2 | `gemini-embedding-2` | $0.20 |

Prices and model availability can change. Do not hard-code prices into request handling or use this table as an entitlement check. OpenCode states that embedding and decision models are billed at list price without the Go discount; the monthly allowance matches the plan amount ($10 Go / $40 Go Plus; Go Teams has its documented per-member allowance). The gateway should not attempt to calculate or enforce this billing.

Official references:

- Go plan and embedding endpoint: https://opencode.ai/docs/console/go/#embedding-models
- Console model documentation, Embedding models section: https://opencode.ai/docs/console/models#embedding-models

## 3. Proposed gateway API

Expose the conventional OpenAI-compatible route on the local gateway:

```text
POST /v1/embeddings
```

If the project already exposes an OpenAI-compatible prefix or versioned API router, register the endpoint consistently with its existing conventions. Avoid adding several speculative aliases. A direct `/embeddings` alias is optional only if existing gateway clients require it.

Example local request:

```bash
curl http://127.0.0.1:8787/v1/embeddings \
  -H 'Authorization: Bearer <LOCAL_OPENCODE2API_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen3-embedding-8b",
    "input": ["hello world", "goodbye"]
  }'
```

The gateway must authenticate the caller using its existing local authentication mechanism. It must then use the configured OpenCode Go upstream credential for the upstream request. Do not forward the caller's local gateway bearer token as the OpenCode credential unless the existing architecture explicitly uses the same credential for both purposes.

## 4. Configuration

Follow the repository's current configuration mechanism (environment variables, YAML, JSON, or config file). Suggested logical settings:

```text
OPENCODE_GO_BASE_URL=https://opencode.ai/inference/go/openai/v1
OPENCODE_GO_API_KEY=<OpenCode Go API key>
```

Then construct the upstream route by joining the configured base URL with `embeddings`:

```text
https://opencode.ai/inference/go/openai/v1/embeddings
```

Requirements:

- Keep the base URL configurable for testing and future compatibility.
- Keep the API key out of source control, response bodies, and logs.
- Reuse the project's existing secret/config loading and validation patterns.
- Fail clearly at startup or on request if the upstream key is missing; do not silently send an unauthenticated request.
- Do not confuse this Go endpoint with the non-Go Console endpoint `https://opencode.ai/inference/openai/v1/embeddings`.

## 5. Request handling

### 5.1 Request body

Accept the OpenAI Embeddings request schema and forward the JSON body to upstream. At minimum support:

- `model` (required string)
- `input` (required string or array of strings)
- `encoding_format` (optional; upstream documentation identifies `float` and `base64` behavior)
- `dimensions` (optional; only meaningful for models that support dimensionality reduction)
- `user` (optional, if supplied by the client/upstream contract)

The official documentation explicitly states:

- `input` may be a string or an array of strings.
- `data` returns one vector per input, in the same order.
- `dimensions` shortens vectors on models that support it, such as `text-embedding-3-small`.
- `encoding_format: "base64"` returns vectors base64-encoded instead of as floats.
- Each input must fit within the selected model's context window.

Implementation rules:

1. Preserve the JSON request body and all supported fields. Prefer raw-body passthrough after validation rather than decoding and rebuilding a reduced struct that could drop future fields.
2. Do not rewrite the model ID, except for an explicit, documented local alias feature already supported by the project.
3. Do not assume every model supports `dimensions`; pass it through and let upstream validate it.
4. Do not split or batch the `input` array in the first implementation. Splitting can alter ordering, usage accounting, limits, and error semantics. Add batching only as a separately designed feature.
5. Do not add `x-opencode-session`; it is not required for embedding requests.
6. Do not silently truncate long inputs. Return the upstream validation error.
7. Apply a configurable request-body size limit consistent with the gateway's existing limits.

### 5.2 Upstream request

Send:

```http
POST {OPENCODE_GO_BASE_URL}/embeddings
Authorization: Bearer {OPENCODE_GO_API_KEY}
Content-Type: application/json
Accept: application/json
```

Forward the request body unchanged (subject to any existing JSON validation). Use the project's shared HTTP client if available. Set connection, response-header, and total request timeouts consistent with the gateway's other upstream calls. Embeddings are ordinary non-streaming HTTP requests; do not implement SSE handling for this route.

Do not forward hop-by-hop headers, the local caller's Authorization header, or untrusted host/forwarding headers. Forward only headers that are intentionally supported by the gateway.

## 6. Response handling

Return the upstream HTTP status and JSON response body to the local caller. The normal OpenAI embeddings response includes fields such as:

```json
{
  "object": "list",
  "data": [
    {
      "object": "embedding",
      "index": 0,
      "embedding": [0.0123, -0.0456]
    }
  ],
  "model": "qwen3-embedding-8b",
  "usage": {
    "prompt_tokens": 2,
    "total_tokens": 2
  }
}
```

The values above are illustrative only. Do not fabricate vectors, token counts, model names, or cost fields.

Requirements:

- Preserve `data` array order and all vector values exactly as returned.
- Preserve `object`, `index`, `model`, `usage`, and any additional upstream fields.
- Support both float arrays and base64-encoded embeddings without decoding/re-encoding them.
- Preserve upstream non-2xx status codes and error JSON where possible.
- Set an appropriate JSON content type when returning JSON.
- Do not convert an upstream error into HTTP 200.
- Do not expose upstream credentials or internal transport details in error messages.

OpenCode's Console documentation notes that the response reports `usage.cost` like other inference requests. Treat this and any future usage fields as opaque upstream response data and pass them through; do not assume the field is always present.

## 7. Error handling

The proxy should preserve upstream status and error payload for errors such as:

- `401`: invalid/missing upstream API key.
- `403`: Go subscription/entitlement or access restriction; the documented entitlement error may indicate that an active Go subscription is required.
- `400` / `422`: invalid model, invalid input, unsupported dimensions/encoding, or context-window violation.
- `429`: rate limit or allowance exhausted.
- `5xx`: upstream service failure.

For gateway-side failures (invalid local authentication, missing configuration, malformed request, request too large, upstream timeout, or connection failure), use the project's established OpenAI-compatible error envelope and appropriate HTTP status. Never return a successful embeddings-shaped response for a failure.

Suggested behavior:

| Failure | Suggested local status |
|---|---:|
| Local caller unauthorized | 401 |
| Malformed JSON / missing required fields | 400 |
| Body too large | 413 |
| Missing upstream key/configuration | 500 (or existing configuration error convention) |
| Upstream timeout | 504 |
| Upstream connection failure | 502 |
| Upstream HTTP error | Preserve upstream status and body |

Avoid automatic retries for non-idempotent-looking requests unless the gateway's retry policy explicitly permits them. If retries are used, limit them to safe transient connection failures and selected 5xx responses, with a small bounded retry count; do not retry 400, 401, 403, 422, or 429 by default.

## 8. Model discovery and routing

OpenCode documents embedding models separately from chat models. They do not appear in the ordinary `/models` list used for chat selection. Therefore:

- Do not inject embedding IDs into the existing chat model list unless the gateway already has a distinct model-capability schema that can label them as `embedding`.
- The first implementation may accept any `model` string and let upstream validate availability. This avoids stale local allowlists.
- Optionally maintain a separate `/v1/embeddings/models` or capability metadata endpoint only if the project has a clear existing pattern and a real source of truth. Do not invent an upstream model-list endpoint for embeddings.
- Do not route embedding requests to the chat-completions endpoint.

## 9. Security and operational requirements

- Require the gateway's normal caller authentication.
- Store the OpenCode Go key in a secret/environment configuration, never in code.
- Redact Authorization headers and API keys from request/response logs.
- Do not log full embedding inputs by default; they may contain private documents or user data.
- Log request ID, selected model ID, latency, upstream status, and safe usage metadata if available.
- Do not log embedding vectors by default; they are large and may encode sensitive semantic information.
- Apply normal rate limiting and concurrency controls.
- Do not add `x-opencode-session` unless OpenCode changes its embedding endpoint requirements.

## 10. Tests and acceptance criteria

Add unit/integration tests using a mock upstream HTTP server. Tests must not require a live OpenCode Go key.

### Required tests

1. **String input:** a single string is forwarded and response is passed through.
2. **Array input:** multiple strings are forwarded; returned vector order is unchanged.
3. **Model passthrough:** all three documented Go model IDs are forwarded exactly.
4. **Optional fields:** `dimensions`, `encoding_format`, and `user` survive forwarding.
5. **Base64 response:** base64 embedding payload is returned unchanged.
6. **Float response:** numeric vectors are returned unchanged.
7. **Auth separation:** local caller auth is validated; upstream Authorization uses the configured Go key.
8. **No session header:** request succeeds without `x-opencode-session`, and gateway does not require one.
9. **Upstream errors:** 400, 401, 403, 429, and 5xx statuses and error bodies are handled correctly.
10. **Timeout/connection failure:** mapped to gateway error responses without leaking secrets.
11. **Secret redaction:** logs do not contain the Go API key or request Authorization value.
12. **No chat-model pollution:** embedding model IDs are not incorrectly exposed as chat models.

### Definition of done

- `POST /v1/embeddings` works through the local gateway.
- A caller can select `text-embedding-3-small`, `qwen3-embedding-8b`, or `gemini-embedding-2` using the model ID from the official Go documentation.
- Upstream request goes to the Go-specific URL, not the non-Go Console URL.
- Vectors, response metadata, status codes, and error bodies are preserved.
- Existing chat completions, Responses API, and WebUI behavior remain unchanged.
- Automated tests cover the cases above.

## 11. Manual smoke test

Set the gateway's configured upstream key using its normal secret mechanism, then call the local endpoint:

```bash
curl -i http://127.0.0.1:8787/v1/embeddings \
  -H 'Authorization: Bearer <LOCAL_OPENCODE2API_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen3-embedding-8b",
    "input": ["FineBI is a business intelligence platform.", "FineBI supports data analysis and visualization."],
    "encoding_format": "float"
  }'
```

Expected result: HTTP 200 and an OpenAI-compatible embeddings response containing two entries in `data`, in the same order as the two inputs. The actual vector dimensions, values, token usage, and cost are determined by the upstream model and must not be hard-coded.

## 12. Implementation note for this repository

This specification intentionally does not prescribe file names, class names, or framework-specific code because the current `opencode2api` source tree has not been inspected in this task. During implementation:

1. Locate the existing OpenAI-compatible router and chat-completions proxy handler.
2. Reuse its authentication, configuration, HTTP client, timeout, logging, and error utilities.
3. Add a dedicated embeddings handler or a generic upstream proxy route, whichever best matches the codebase.
4. Add tests alongside the existing API handler tests.
5. Keep the change isolated so existing chat and Responses API routes are unaffected.
