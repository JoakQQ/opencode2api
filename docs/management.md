# Management console: model rotation, Go quota, and API keys

This guide covers how the management pages are used, configured, and how they report errors.

## Models and quota

Management page: `/#model-quotas`. Once signed in it reads `GET /api/model-quotas`.

- Models come from the OpenCode Go catalogue the gateway syncs; the page supports searching for and copying model IDs.
- The gateway queries `https://opencode.ai/zen/go/v1/usage` with the configured Go keys. The browser never receives an upstream key.
- The page shows the remaining percentage and reset time for the 5-hour, weekly, and monthly windows. The endpoint exposes only the shared plan windows: it provides no per-model balance and never estimates money or request counts. Quotas of several keys are not summed.
- Successful responses are cached for 60 seconds and failures for 15 seconds; on failure the last successful reading is kept and clearly marked as historical data. With no successful reading the page shows unknown.
- While the page is open it queries once every 60 seconds; leaving the page or hiding the browser stops the polling. Manual refreshes are still bounded by the server-side cache.

Reference: [OpenCode's official quota endpoint source](https://github.com/anomalyco/opencode/blob/dev/packages/console/app/src/routes/zen/go/v1/usage.ts). The endpoint is not part of the official API documentation, so a changed response shape shows a query error instead of invented quota.

## API key management

Management page: `/#api-keys`. It lists, generates, customizes, renames, enables, disables, and deletes keys, and copies the environment variable after creation.

- It manages the gateway's `server_keys`; OpenCode Go upstream keys are never modified.
- Generated keys carry 192 random bits; custom keys are 16–256 characters from letters, digits, `-`, `_`, or `.`.
- The list shows only the name, tail, state, usage, and creation time. After creation the full key and its environment variable can be copied.
- Usage counts requests and tokens per authenticated key. The API key list keeps two windows, the last hour and lifetime; `usage.window.server_keys` and `usage.lifetime.server_keys` from `GET /api/monitor` report the Usage-tab window and the lifetime total, and their entries sum to the same aggregate. Playground traffic is bucketed separately as `playground` and belongs to no key. Metrics and log events are written by default to the SQLite file at `storage.path` (default `/storage/opencode-core.db`): they survive hot reload and are restored after a restart, and rows older than 48 hours are pruned every hour while the lifetime counters are kept.
- Changes apply hot; after a key is disabled or deleted, new requests return 401. In-flight requests may finish. At least one enabled key must remain.
- Every management endpoint requires a session, and writes also require a CSRF token.
- Metadata is stored in the original configuration as `server_key_metadata`, keyed by key fingerprint; existing `server_keys` are compatible automatically.
- Per-key model permissions, usage quotas, and expiry are not implemented.

## WARP egress for free models

The bottom of the "Proxy nodes" page in the WebUI adds a WARP switch, backed by `warp.enabled` (off by default), `warp.pool_size` (1–32, default 2; the upper bound on exits, with the active count following concurrency), `warp.stack` (default `ipv6`; `ipv4` or `dual` are also accepted), and `warp.source_ipv6` (an outer IPv6 source prefix, requiring `stack = "ipv6"`). When enabled, the anonymous lane, the free-model Zen-key fallback, the free-model availability probes, and the anonymous catalogue refresh all egress through userspace Cloudflare WARP and no longer use any proxy; Go-plan keys and paid models keep their existing proxy bindings.

- The host or container must be able to send UDP to Cloudflare's endpoint (port `2408`); no TUN device, root, `NET_ADMIN`, or routing-table change is needed.
- An exit answered with 429/403 is retired on its first failure and re-registered in the background immediately; a free-model request keeps switching exits and only returns the last 429 to the client once `retry.max_attempts` is spent. The active exit count follows concurrency (one per in-flight request plus a spare, capped by `warp.pool_size`), and surplus exits idle for 60 seconds are reclaimed. The free quota is scoped to the exit's network block: IPv6 exits are deduplicated by `/64` and IPv4 exits by `/32`, and both are cooled for 30 minutes. Cloudflare WARP's shared IPv4 egress is usually exhausted, so `warp.stack` defaults to `ipv6`; with `warp.source_ipv6` (a `/64` routed to the host, bound on Linux through `IPV6_FREEBIND`) giving every exit its own source address, a replacement exit only obtains a new public address that way.
- If WARP cannot initialize, startup fails outright; enabling it through the WebUI returns an error for bad values while the previous runtime keeps serving, with no silent fallback to proxies.
- The `warp` block in `/healthz` and `resources.warp` in `/api/monitor` report the stack, exit counts, active/standby/cooling counts, and exit addresses. When WARP is enabled but no exit is active the health check answers 503, and in that case `no_healthy_proxies` no longer blocks readiness when no Go key is configured.

## Verification and builds

```bash
go test ./...
go test -race ./...
go vet ./...
for f in webui/js/*.js webui/js/pages/*.js; do node --input-type=module --check < "$f"; done
go build -trimpath -o opencode2api ./cmd/opencode2api
```

Before upgrading, back up the binary, the configuration, and the rotation state, then stop the service and replace the executable. Older versions do not recognise the added `server_key_metadata` and `rotation` fields; when rolling back, restore the old binary and the old configuration together.

## SOTA / sweet model rotation

Management page: `/#rotation`. Calls keep using the gateway's base URL (default `http://127.0.0.1:8080/v1`) and an existing API key, with `model` set to `sota` or `sweet`. Both aliases are listed in `/v1/models` and support Chat Completions, Responses, and Messages, including streaming for all three protocols. The original request path for real model IDs is unchanged.

- SOTA members are fixed: `deepseek-v4.1-flash`, `glm-5.3`, `grok-4.6`, `kimi-k3`, `qwen3.8-max`, in that order by default.
- The sweet group holds the remaining Go models, starting with `glm-5.3-flash` and then sorted by name. New models are appended at the end; retired models and models with unsupported protocols stay in the list but are not called.
- Each group shares one current model. A success keeps the position; after a failure switch, the next request starts from the new position. Concurrent requests carry a position version so an older request cannot move the cursor back, and a manual switch does not interrupt an in-flight answer.
- Ordinary errors allow at most 3 attempts per model (the first included), and one request may spend at most 3 models' worth of ordinary-error budget. After a model's retries are exhausted the group pauses for 15 seconds. Group requests do not use the regular key retry, cross-tier fallback, automatic redirect, or stale-reasoning replay, and a single failing model does not pause the whole Go key.
- A 429 or an explicit quota error jumps straight to the next model without adding to the ordinary-error model count; ordinary errors that already happened still count. The whole group may be walked, and a request never repeats a model. The pause honours Retry-After and defaults to 60 seconds when it is absent. The shared plan quota is informational only; switching models does not guarantee a remaining balance.
- The overall timeout follows `retry.timeout_seconds`. `rotation.first_output_seconds` defaults to 30 seconds and bounds each wait for the first valid output without limiting a normal stream that has already started.
- Streaming start/role/usage preamble events are committed only once a valid output appears or the stream ends normally. An error before the commit can retry; an interruption after the commit keeps the output already sent, appends the protocol's SSE error, ends the response, and then advances the position so the next request uses the next model. A single response never concatenates a replacement answer. Caller cancellation neither pauses the group nor switches models.
- The `model` field of JSON/SSE responses keeps the requested alias, and `X-Resolved-Model` names the real model. Usage is recorded against the real upstream model. Rotation logs and the management page keep only classified error reasons, never upstream error bodies, request bodies, or keys. The last 100 attempts and 30 switches per group are retained.

Configuration lives in the `rotation` field of `config.json`: `sota` and `sweet` each have `alias` and `order`. State is written atomically to `config.json.rotation.json` (mode 0600) separately, covering the current position, version, dynamically appended order, pause deadline, and recent records. Changing keys or other settings reuses the same rotation state manager. The page shows a notice when the state write fails, and the first failure at startup refuses to start.

The management endpoints require an existing admin session, and writes also require CSRF:

- `GET /api/rotation`: group configuration, availability, pause deadlines, and the switch and attempt records.
- `PUT /api/rotation`: submits `alias` and the full `order` for `sota` / `sweet`, plus `first_output_seconds`; aliases must be unique and must not collide with a real model ID, and members must not span groups.
- `POST /api/rotation/current`: `{"group":"sota","model":"glm-5.3"}` immediately sets the current model and clears that model's temporary pause.

Validation: `go test -race ./...`, `go vet ./...`, and the Node.js syntax loop above. Simulated-upstream tests cover the 9-attempt ordinary-error ceiling, skipping after more than 3 quota failures, mixed error budgets, Retry-After, one model failing without pausing the key, cross-protocol conversion for the three entry protocols, position retention after success, alias discovery and usage attribution, malformed responses, pre-output errors, first-output timeout, post-output failures, caller cancellation, manual switches not disturbing in-flight answers, concurrent version protection, retirement, and restart recovery. Before going live, validate admin login, CSRF, page ordering and saving, alias conflicts, hot key updates, and real Go requests with a separate configuration and port. Tests and production configuration, credentials, logs, and runtime state should never be committed to the repository.
