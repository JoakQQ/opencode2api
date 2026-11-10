# opencode2api

[English](README.md) | [简体中文](README.zh-CN.md) | [繁體中文](README.zh-HK.md) | [Tiếng Việt](README.vi.md) | [Italiano](README.it.md)

使用 Go 編寫的 **OpenCode Zen / Zen Go API 網關**。對外提供 Chat Completions、Responses 和 Anthropic Messages 接口，按上游原生協議轉換請求，並統一管理上游 Key 與代理。

WebUI 內嵌在可執行檔案中，執行服務無需 Node.js 或資料庫。

> **Fork 說明：** 本儲存庫是 [opencode2api](https://github.com/jasonxu114514/opencode2api) 的分支，原專案位址見連結。

## 功能

- 三種推理協議之間的普通 JSON 回應與 SSE 流式轉換。
- 文本、圖片、reasoning、函數工具、工具呼叫和工具結果；目標協議允許時支援檔案內容轉換。
- 獨立的 Zen / Go Key 池，可設定優先級、重試和會話親和性。
- 使用 OpenCode `public` 憑證的可選 Zen 匿名通道。
- 可選的用戶態 Cloudflare WARP 出口，免費模型流量無需 TUN 或 root 權限。
- 直連、HTTP、HTTPS、SOCKS5、SOCKS5H 連接，以及代理檔案。
- 動態模型發現、原生協議與能力資訊、磁碟快取。
- 獨立管理連接埠，支援設定編輯、Playground、路由診斷、Token 統計和即時日誌。
- 設定熱重載：先驗證新設定，再切換新請求使用的 Gateway。
- Go 共享套餐額度看板，顯示剩餘百分比、重置時間和可用模型。
- API Key 生成、自定義、命名、啟停和刪除，變更立即生效。
- `sota` / `sweet` 分組別名，支援模型排序、故障切換、流式輸出保護和狀態恢復。

新增頁面及呼叫範例見[管理控制台說明](docs/management.zh-CN.md)。

## 快速開始

從 [GitHub Releases](https://github.com/JoakQQ/opencode2api/releases) 下載可執行檔案，或使用 **Go 1.24 及以上版本**編譯：

```bash
git clone https://github.com/JoakQQ/opencode2api.git
cd opencode2api
cp config.example.json config.json
go build -o opencode2api ./cmd/opencode2api
```

啟動前編輯 `config.json`：

1. 將 `server_keys` 替換為自己的本地 API Key。
2. 可選：填入 Zen 或 Go Key；範例已啟用匿名通道，並清空兩個上游 Key 陣列。
3. 替換 `webui.password`。範例設定啟用了 WebUI，用戶名為 `admin`。

```bash
./opencode2api -config config.json
```

Windows 下使用 `Copy-Item config.example.json config.json` 複製設定，執行 `go build -o opencode2api.exe ./cmd/opencode2api` 編譯，再執行 `.\opencode2api.exe -config config.json`。

範例設定的 API 監聽 `127.0.0.1:8080`，WebUI 監聽 `0.0.0.0:8081`。本機可存取 `http://localhost:8081`。透過網絡存取管理介面時，請限制存取範圍，並使用 HTTPS 反向代理。

命令行參數：

| 參數          | 預設值        | 用途                  |
| ------------- | ------------- | --------------------- |
| `-config`     | `config.json` | 設定檔案路徑。        |
| `-listen`     | 未設定        | 覆蓋 API 監聽位址。   |
| `-web-listen` | 未設定        | 覆蓋 WebUI 監聽位址。 |

設定所在目錄需要可寫，以便遷移密碼、儲存設定和寫入模型快取。

## Docker 部署

`compose.yaml` 中的映像仍指向原專案倉庫；要執行本分支程式碼，請按下文在本機建置映像，或發佈自己的映像並透過 `OPENCODE2API_VERSION` 指定。

```bash
cp config.example.json config.json
# 啟動前填寫 Key，並替換 WebUI 密碼。
docker compose up -d
docker compose logs -f
```

Compose **僅在首次啟動時**將宿主機設定導入 `opencode2api-state` 命名卷。後續建議透過 WebUI 修改設定。需要重新導入宿主機檔案時：

```bash
docker compose cp config.json opencode2api:/var/lib/opencode2api/config.json
docker compose restart
```

容器內的服務進程使用普通用戶執行。Compose 啟用唯讀根檔案系統，並提供可寫狀態卷和臨時 `/tmp` 檔案系統。

WARP 只需要出站 UDP（Cloudflare 端點連接埠 `2408`），因此映像的普通用戶、無 `NET_ADMIN` 設定無需改動；不要為此改用 host 網絡或添加 `--cap-add`。如果容器網絡禁止 UDP，啟用 WARP 會在啟動時失敗。

| Compose 環境變量            | 預設值   | 作用                                 |
| --------------------------- | -------- | ------------------------------------ |
| `OPENCODE2API_VERSION`      | `latest` | 映像標籤；可指定發行標籤來固定版本。 |
| `OPENCODE2API_PORT`         | `8080`   | 映射到容器 8080 的宿主機連接埠。       |
| `OPENCODE2API_WEBUI_PORT`   | `8081`   | 映射到容器 8081 的宿主機連接埠。       |
| `OPENCODE2API_LISTEN`       | 未設定   | 顯式覆蓋容器內 API 監聽位址。        |
| `OPENCODE2API_WEBUI_LISTEN` | 未設定   | 顯式覆蓋容器內 WebUI 監聽位址。      |

後兩項在容器內對應 `LISTEN_ADDRESS` 與 `WEBUI_LISTEN_ADDRESS`。值為空時使用設定檔案中的位址。首次初始化設定時，入口腳本會將範例 API 位址 `127.0.0.1:8080` 改為 `0.0.0.0:8080`，使其能夠透過發佈連接埠存取。

修改宿主機連接埠不會改變容器內監聽位址。如果修改容器內 API 連接埠，還需要同步調整連接埠映射和映像健康檢查；預設健康檢查使用 8080 連接埠。

構建並執行本地映像：

```bash
docker build -t opencode2api:local .
docker volume create opencode2api-state
docker run -d --name opencode2api \
  -p 8080:8080 -p 8081:8081 \
  -e CONFIG_SEED_PATH=/run/config/opencode2api.json \
  -v "$(pwd)/config.json:/run/config/opencode2api.json:ro" \
  -v opencode2api-state:/var/lib/opencode2api \
  opencode2api:local
```

在 Docker 中使用 `proxyfile` 時，代理檔案也需要掛載到容器內設定指定的位置。

## API 呼叫

`server_keys` 用於客戶端呼叫本地網關，與 `zen_keys`、`go_keys` 相互獨立，不會作為上游認證憑證發送。

請求可以使用 `Authorization: Bearer YOUR_LOCAL_API_KEY` 或 `x-api-key: YOUR_LOCAL_API_KEY`。健康檢查無需認證。

| 方法 | 路徑                   | 用途                       |
| ---- | ---------------------- | -------------------------- |
| GET  | `/v1/models`           | 目前設定下可以路由的模型。 |
| POST | `/v1/chat/completions` | Chat Completions。         |
| POST | `/v1/responses`        | Responses。                |
| POST | `/v1/messages`         | Anthropic Messages。       |
| POST | `/v1/systemone`        | System One 結構化決策。    |
| GET  | `/healthz`             | 就緒狀態與資源彙總。       |

先獲取可用模型：

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY"
```

將以下範例中的 `MODEL_ID` 替換為模型列表返回的 ID。

**Chat Completions**

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","messages":[{"role":"user","content":"Hello"}]}'
```

**Responses**

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","input":"Hello"}'
```

**Anthropic Messages**

```bash
curl http://localhost:8080/v1/messages \
  -H "x-api-key: YOUR_LOCAL_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","max_tokens":512,"messages":[{"role":"user","content":"Hello"}]}'
```

需要流式回應時，在請求體中加入 `"stream": true`，並使用 `curl -N`。回應透過 `x-request-id` 提供請求關聯標識。API 請求體上限為 32 MiB。

**System One**

```bash
curl http://localhost:8080/v1/systemone \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","state":"1 + 1 = 2","questions":{"arithmetic":{"type":"noul","instructions":"Is 1 + 1 equal to 2?"}}}'
```

System One 將自由文本狀態與類型化問題配對，並返回類型化答案，因此其負載與 chat/responses 橋接結構完全不同，只做原樣轉發，不做任何轉換。只有原生協議為 System One 的模型才能在本接口提供服務；在消息接口（`/v1/chat/completions`、`/v1/responses`）上提交的決策負載同樣原樣轉發，而不經過轉換。上游的流式回覆按原樣透傳，不做中斷恢復。

### 協議兼容邊界

客戶端協議與上游原生協議相同時，會保留供應商特有欄位。跨協議請求經過統一中間結構轉換，部分參數沒有等價表達。例如，Chat 的 JSON 輸出約束與 `seed` 不會被轉發到 Anthropic Messages。不支援的內容塊或工具類型可能被拒絕。

網關僅實現上表中的接口，不提供 embeddings、檔案上傳、圖像生成、Responses 查詢或取消接口。網關不儲存會話歷史；客戶端需要提交歷史，或使用實際上游支援的相關能力。

## 路由與模型

### 模型發現

服務按設定週期刷新 Zen / Go 的 `/v1/models` 和 OpenCode [能力目錄](https://models.opencode.ai/api.json)，分別記錄兩個 Tier 的原生協議與模型限制。OpenCode 的 Zen / Go 文檔作為協議發現的回退來源。

可透過 `models.protocols` 覆蓋自動發現結果：

```json
{
  "models": {
    "refresh_seconds": 300,
    "protocols": {
      "custom-model": "chat"
    }
  }
}
```

協議值僅允許 `chat`、`responses`、`anthropic`。使用不支援的原生協議的模型會從模型列表中隱藏，除非設定了覆蓋值。

成本、棄用資訊及模型限制來自官方 [OpenCode 中繼資料位址](https://models.opencode.ai/api.json)，每 24 小時刷新一次。能力目錄中的限制優先；中繼資料中的 `limit.context`、`limit.input` 和 `limit.output` 僅補充缺失欄位，透過 `/v1/models` 的 `context_window` / `context_length`、`max_input`、`max_output` 公開。每個 HTTP 客戶端的拉取超時為 30 秒，刷新失敗時保留已有數據。升級後繼續兼容原有 `.models.dev.json` 快取檔案名，無需刪除快取。

中繼資料給出價格時，`/v1/models` 的每個條目還會帶上 `cost`（每百萬 token 的美元價格：`input`、`output`、`cache_read`、`cache_write`，以及 OpenClaw 使用的 `cacheRead` / `cacheWrite`）。推理模型會在 `reasoning_efforts` 和 OpenClaw 讀取的 `compat.supportedReasoningEfforts` 中列出可用檔位：`minimal` 到 `max`；思考強度為 `auto` 時截止到探測得到的上限。強制或探測得到的檔位透過 `default_reasoning_effort` 給出。快取價格欄位會在下一次中繼資料刷新後出現。

### 匿名通道與回退

啟用 `anonymous: true` 後，滿足以下任一條件的模型可以進入 Zen 匿名通道：

- 模型 ID 包含 `free`，不區分大小寫。
- OpenCode 中繼資料中的輸入、輸出成本均為零，且模型未棄用。

這只是網關的路由判斷，上游仍可能拒絕或限制請求。匿名請求在上游認證頭中使用 `public` 憑證。

路由順序：

1. 符合條件的模型最多嘗試 `performance.max_anonymous_exits` 個可用匿名代理，每個代理一次；每個代理先走 v2 `/inference`，再走 v1 `/zen`。
2. 按 `prefer` 順序嘗試認證通道，僅包含已設定 Key 且可以提供該模型的 Tier。
3. 每個認證 Tier 分別使用 `retry.max_attempts` 預算。

匿名階段不受 `retry.max_attempts` 截斷，但所有階段共享請求總超時。網絡錯誤、認證失敗、限流和伺服器端錯誤可以觸發出口輪換。請求級 `400`/`413`/`422`（例如 `context_length_exceeded`）會立即結束請求：換出口或換 Tier 都無法改變請求本身。IP 級 abuse `403` 只淘汰目前出口；所有出口都被封時停止掃描，已設定的 Go Key 仍可繼續服務。

設定 `models.anonymous_backoff` 後，連續 `threshold` 次匿名失敗或所有出口都被 IP 封禁會暫停整條匿名通道 `base_hours`，之後每次再觸發翻倍，直到 `max_hours`。單個非濫用 `403` 或 `429` 不會立即熔斷，只計入連續失敗。暫停期間不取代理、不發任何上游請求（包括模型目錄刷新和可用性探測），路由直接落到認證通道。視窗到期後只允許一個請求做半開探測：成功關閉熔斷並恢復匿名通道，失敗則開啟下一個更長的視窗。省略該設定塊時熔斷關閉。

每個 Tier 使用自己的原生協議編碼請求。啟用 `performance.first_event_timeout_seconds` 後，會先等待完整的 SSE data 事件再向客戶端交付回應；註釋心跳不算首事件。首事件前超時或斷開會沿用現有 Key / 代理重試預算，交付後不再重放。預設 `0` 關閉，可對接受請求後無事件的上游嘗試設定為 `15` 秒。它限制首事件而非首個文本 token，不適用於 System One；輪轉別名使用獨立的 `rotation.first_output_seconds`。識別到過期的 Responses reasoning 引用時，可執行一次修復重試；指定 Key 的診斷不會執行該重試。

只有匿名通道可用時，`/v1/models` 僅展示符合匿名條件的模型。

免費模型不需要 Zen Key：啟用 `anonymous: true` 後由匿名通道提供，已設定的 Key 只作為回退。如果想保留 Key 但只公開免費模型，設定 `models.free_only: true`：付費模型不再出現在 `/v1/models`，直接請求會被拒絕，輪轉別名只保留免費成員；免費模型仍可回退到已設定的 Key。

### 免費模型 WARP 出口

啟用 `warp.enabled` 後，免費通道上的所有免費模型請求——匿名通道、透過 Zen Key 的免費模型回退、免費模型可用性探測，以及匿名模型目錄刷新——都改走用戶態 Cloudflare WARP 隧道，不再使用任何代理，因此上游看到的是 WARP 出口位址，而不是設定的節點。Go 套餐 Key 與付費模型仍使用原有代理綁定。

隧道執行在網關進程內：註冊 WARP 設備，透過 UDP 與 Cloudflare 建立 WireGuard 連接，並在用戶態網絡棧中終結。它不需要 TUN 設備、root、`NET_ADMIN` 或修改路由表，但宿主機（或容器）必須能向 Cloudflare 端點發送 UDP（連接埠 `2408`）。註冊與 `warp=on` 的 trace 探測在啟動時完成；WARP 無法初始化時啟動直接失敗，不會靜默回退到代理。透過 WebUI 啟用同樣採用失敗即拒絕：參數錯誤會返回錯誤，舊執行時繼續服務。

`warp.pool_size`（1–32，預設 `2`）是**上限**而不是固定數量。第一個出口同步註冊、決定服務是否 ready；之後池子在後台預註冊上限的一半（至少 1 個），突發流量可以直接拿到預熱出口而不用等註冊。活躍出口再按並行調整：每個在途請求一個出口，另加一個備用，最多不超過上限；空閒 60 秒的多餘出口會被回收。出口第一次收到上游 429 或 403 即被淘汰，由預註冊的出口立刻頂上——免費模型請求會持續切換到其他出口，直到用完 `retry.max_attempts` 次才把最後一個 429 返回客戶端；被淘汰的出口馬上在後台重新註冊。註冊仍然串行，節流改由 Cloudflare 的 429 / Retry-After 決定，而不是固定間隔。若機房能給出的不同出口網段少於預註冊目標，池子會退避而不是無限註冊；自動免費模型探測在沒有備用出口時跳過，並在一輪探測淘汰出口後停止本輪。`warp.stack` 預設 `ipv6`，可選 `ipv4` 或 `dual`。

上游的免費額度按**出口網段**計算，所以只有替換出口能拿到不同網段時輪換才有意義：IPv6 出口按 `/64` 去重並冷卻 30 分鐘，IPv4 出口按 `/32`。Cloudflare WARP 的 IPv4 出口通常是同一個共享 NAT 位址，常被其他用戶用盡；IPv6 網絡棧的出口則由外層 WireGuard 源位址決定。把 `warp.source_ipv6` 設為路由到本機的 IPv6 前綴（通常是 `/64`，`warp.stack` 預設已是 `ipv6`），每個出口就會從該前綴綁定自己的源位址。Linux 下該位址透過 `IPV6_FREEBIND` 綁定（網關已自動設定），或要求位址已分配到網卡。不會預先探測位址族：握手先嚐試 IPv6 端點，全部失敗時自動回退到 IPv4 端點（此時出口為共享位址）。留空則共用本機預設源位址。某些網絡的 WARP 出口池本身已被用盡，此時每個新出口也會返回 429，網關會持續輪換出口，但仍會在嘗試次數用盡後返回 429。`/healthz` 會返回 `warp` 狀態塊；啟用 WARP 但沒有任何活躍出口時返回 503。只有 WARP 關閉或存在 Go Key 需要代理時，`no_healthy_proxies` 才會阻塞就緒。診斷請求會佔用出口，但不會輪換或淘汰它。關閉 WARP 後，免費通道恢復使用代理。

### 官方系統提示詞

每個經過整形的免費模型請求都會攜帶 OpenCode CLI 自身的系統提示詞——基礎指令、`<env>` 環境塊和內建 skill 列表——因為免費層級會校驗請求是否具有 agent 形態。v1 `/zen` 使用 1.x 工具名，`/inference` 與 `/zen/go` 使用 2.x 工具名。環境塊包含請求的模型、網關工作目錄、平台和 UTC 日期。

注入按協議處理：Chat 的系統與 developer 消息會合併為一條位於最前的 `system` 消息；Anthropic 寫入 `system`（字符串，或呼叫方提供塊列表時在最前插入文本塊）；Responses 寫入 `instructions`。如果系統內容中已包含標記 `You are opencode, an interactive CLI tool`，請求會原樣透過，因此已經發送官方提示詞的客戶端不會被重複注入。透過 Zen 或 Go Key 提供的免費模型也會接受同樣的整形，只是仍然從對應 Tier 的代理出網。

### 工具整形

每個經過整形的免費模型請求中，網關會把客戶端聲明的每個具名工具改名為不透明的線上名稱 `clint_` 加上客戶端名稱 SHA-256 的前 12 個十六進制字符（衝突時追加 `_2`、`_3` 等後綴），並追加該通道的完整官方工具集：`/zen` 使用 1.x 定義（`bash`、`edit`、`glob`、`grep`、`read`、`skill`、`task`、`todowrite`、`webfetch`、`websearch`、`write`），`/inference` 與 `/zen/go` 使用 13 個工具的 2.x 定義。沒有 `tools`、`tools` 為空陣列或使用舊版 `functions` 陣列的請求同樣會補全官方工具集；`tool_choice`、舊版 `function_call` 和工具呼叫歷史會同步改寫。Responses 內建工具和 Anthropic 伺服器端工具保持不變。

回應（包括流式與非流式，以及拆分到多個 Chat 分塊中的工具名）會恢復為客戶端聲明的名稱。只有在客戶端聲明瞭等價工具時，官方工具名才會映射到客戶端名稱；否則回應中的名稱保持原樣。整形請求歷史時，未知工具呼叫以及沒有客戶端等價工具的官方呼叫會連同配對的結果一起移除；若具名 `tool_choice` 指向被移除的工具，也會一併移除。

### 免費模型可用性

只探測 ID 含 `free`（不區分大小寫），或中繼資料輸入、輸出價格均為零且未棄用的模型，按模型原生協議發送簡短探測請求。先嚐試已啟用的匿名通道，再嘗試一個設定的 Zen Key；啟用 `warp.enabled` 時，每條探測通道都改走 WARP 而不是代理，無法取得出口時結果為 `warp_unavailable`，視為不確定。任一通道返回完整有效回應即視為可用，只有所有實際嘗試的通道都明確返回模型不存在或已下架時才自動禁用，從模型列表與路由中移除。認證失敗、429、超時、網絡錯誤、5xx 和回應格式異常均視為結果不確定，不新增禁用，1 小時後複測。探測不修改生產 Key / 代理的冷卻狀態，也不計入請求統計，但仍會存取上游並可能消耗免費額度。

System One 使用原生 `noul` 類型詢問“1+1 是否等於 2”，不注入聊天或工具列位。模型列表標明 `native_protocol` 和 `supported_endpoints`；僅在設定 Go Key 且有可用模型時展示輪轉別名。升級時解除 v1.3.6 舊探測生成的禁用並立即安排複測，避免非法會話 ID 造成的誤判繼續保留。

週期硬編碼：正常模型每 **1 小時**探測，明確失效模型每 **24 小時**複測。已禁用模型到期複測遇到臨時錯誤時，保留原禁用原因並在 24 小時後重試；複測成功自動恢復。也可在 WebUI「免費模型可用性」頁立即恢復啟用，進入手動控制：保持啟用且不再自動探測，也不會被在途或後續探測結果覆蓋；如需回到自動探測，在同一頁面按「切回自動」，下一輪調度即安排探測。舊版本留下的 `manually_enabled` 記錄在啟動時自動遷移為手動控制。沒有可用 Zen / 匿名通道，或推理協議不支援探測時跳過，不自動禁用。

手動控制也會暫停 `reasoning.effort=auto` 的檔位探測；已驗證檔位超過 2 小時後不再強制下發。切回自動後可重新探測檔位。

狀態儲存在 `config.json.<上游位址指紋>.availability.json`，重啟後保留，按 Zen 上游 URL 隔離。後台每分鐘檢查到期項，跳過手動控制的模型，串行探測其餘模型，每條通道最多等待 60 秒。

管理接口：`GET /api/models/availability` 查看狀態；`POST /api/models/probe` 將所有自動探測的免費模型標記為到期並立即開始一輪探測，WebUI「刷新狀態」按鈕即呼叫它（真正的重新探測，而不是隻重載快取視圖）；`POST /api/models/restore` 攜帶 `{"model":"MODEL_ID"}` 恢復並進入手動控制；`POST /api/models/manual` 攜帶 `{"model":"MODEL_ID","manual":true|false}` 在手動與自動之間切換。均需管理登入，寫入接口還需 CSRF 校驗。

### 會話與代理

Key 初始化時均衡分配到代理。真實流量可以觸發代理檢查、Key 重新綁定和失敗冷卻。已經標記異常的代理，每 15 分鐘透過 Cloudflare trace 複查一次。

服務使用穩定會話雜湊選擇首選 Key 或匿名代理。建議透過 `x-session-id` 區分獨立會話，同時支援 `x-opencode-session`、`x-session-affinity`、`session_id`、`conversation-id`、`conversation_id` 請求標頭，請求體中的 `conversation_id`、`metadata.session_id`，以及 Claude Code `metadata.user_id` 中的會話 ID。

網關自行生成的會話與項目標識按呼叫方的伺服器端 Key 隔離，不同 Key 不會共享上游會話、首選 Key 或匿名代理；客戶端自帶的官方 OpenCode 會話（`ses_…`）原樣保留；網關自行派生的會話標識按進程加鹽，重啟網關後會用新的標識，而不會沿用上一輪進程的上游會話。沒有顯式會話 ID 時，使用系統提示詞和第一條用戶消息生成會話標識，因此同一 Key 下系統提示詞和開場內容都相同的會話仍可能共享親和性。需要互相隔離的用戶應各自使用獨立的伺服器端 Key。調整 Key 或代理池成員後，原有會話選擇的節點可能變化。

失敗冷卻按指數增長，最高為 `performance.failure_cooldown_seconds` 的八倍；如果上游的 `Retry-After` 更長，則使用更長時間。所有認證 Key 都處於冷卻時，路由仍可能嘗試最早結束冷卻的 Key；仍在冷卻的匿名節點會被跳過。

## 設定參考

完整起始設定見 [config.example.json](config.example.json)。設定支援 `//` 和 `/* ... */` 註釋，未知欄位和無效取值會被拒絕。

### Key、監聽位址與路由

| 欄位                        | 預設值或要求                                                                          |
| --------------------------- | ------------------------------------------------------------------------------------- |
| `listen`                    | `127.0.0.1:8080`。                                                                    |
| `server_keys`               | 至少一個本地 Key。                                                                    |
| `zen_keys`、`go_keys`       | 未啟用匿名模式時，至少需要一個上游 Key。                                              |
| `anonymous`                 | `false`。                                                                             |
| `warp.enabled`              | `false`；免費通道流量透過 Cloudflare WARP 出口，不使用代理。                          |
| `warp.pool_size`            | `2`；WARP 出口上限（後台預註冊一半，活躍出口按並行調整），1–32。                    |
| `warp.stack`                | `ipv6`；可選 `ipv6`、`ipv4`、`dual`。                                                 |
| `warp.source_ipv6`          | 空；每個出口綁定的外層 IPv6 源前綴（`/64`），要求 `stack = "ipv6"`。                  |
| `prefer`                    | `go`；可選 `go`、`zen`。                                                              |
| `upstream.zen`              | `https://opencode.ai/zen`。                                                           |
| `upstream.go`               | `https://opencode.ai/zen/go`。                                                        |
| `upstream.anonymous`        | `https://opencode.ai/inference`；匿名通道優先使用，失敗後回退到 `/zen`。             |
| `proxies`                   | 兩個代理來源都為空時，使用 `["direct"]`。                                             |
| `proxyfile`                 | 可選；相對路徑基於設定檔案所在目錄解析。                                              |
| `models.refresh_seconds`    | `300`；最小為 1。                                                                     |
| `models.protocols`          | `{}`；按模型 ID 覆蓋原生協議。                                                        |
| `models.free_only`          | `false`；即使設定了 Zen / Go Key，也只公開和路由免費模型。                            |
| `models.active_probes`      | `false`；關閉時透過實際請求被動追蹤模型可用性，不主動發起測試請求。設為 `true` 開啟主動探測。 |
| `models.anonymous_backoff`  | 預設關閉；`{threshold, base_hours, max_hours}`，預設 6、1、24。                       |
| `reasoning.effort`          | 空（關閉）；可選 `auto`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`none`。 |
| `reasoning.effort_by_model` | `{}`；按模型 ID 覆蓋 `reasoning.effort`。                                             |

### 強制思考強度

`reasoning.effort` 在請求自身沒有指定思考強度時，指定發往上游的強度；
`reasoning.effort_by_model` 按模型 ID 覆蓋它。它對三種協議、兩個轉換方向都生效，
對跨協議請求尤其重要：Anthropic 的 `thinking.budget_tokens` 只能被 Chat 與
Responses 近似表達，而強制指定的強度可以到達單獨靠預算表達不出的檔位。

```json
{
  "reasoning": {
    "effort": "high",
    "effort_by_model": { "claude-opus-5": "max" }
  }
}
```

客戶端顯式關閉始終優先。Anthropic 原生 `thinking.type="disabled"` 和 Chat/Responses 的 effort
`"none"` 都會在協議轉換後保持關閉，不會被設定的預設強度重新開啟。

兩個方向的強度都會如實保留：顯式的 `output_config.effort` 不會再被 `thinking`
塊遮蔽，`budget_tokens` 保持其檔位（`8192` 為 `high`，`32000` 為 `xhigh`，
兩者可區分），發往 Responses 時強度會以 `reasoning.effort` 形式傳遞而不再丟失。

WebUI 的設定中心也提供預設強度和按模型覆蓋。管理 API 的 `GET /api/config`
會返回 `reasoning`，`PUT /api/config` 接受同名對象；省略該欄位會保留現有值，
發送空對象則會清除它。

代理支援 `direct`、`http://`、`https://`、`socks5://` 和 `socks5h://`，URL 可包含認證資訊。設定內代理先載入，再追加 `proxyfile` 內容，並按首次出現的順序去重。

代理檔案每行一個位址，允許空行和註釋：

```text
# 首選代理
http://user:password@127.0.0.1:7890
socks5://127.0.0.1:1080  # 備用代理
direct
```

支援 `#`、`;`、`//` 註釋標記，標記需要位於行首或空白字符之後。

#### 輪換代理池

在條目末尾追加 `#pool`（或在開頭加 `pool+`）即可聲明該位址是輪換代理池（例如 [Resin](https://github.com/Resinat/Resin)），每次新建 TCP 連接都會從不同出口 IP 發出：

```text
http://127.0.0.1:7890#pool
socks5://127.0.0.1:1080#pool
pool+http://127.0.0.1:7891
```

代理池條目的處理方式與靜態代理完全不同：

| 行為         | 靜態代理                                    | `#pool` 條目                                              |
| ------------ | ------------------------------------------- | --------------------------------------------------------- |
| 連接複用     | Keep-alive + HTTP/2 多路複用（`performance.*` 連接池參數） | 每次請求新建 TCP + CONNECT 隧道（`DisableKeepAlives`，禁用 HTTP/2） |
| 失敗冷卻     | 15s–120s 指數退避，並受 `Retry-After` 影響  | 無冷卻：下一次請求直接換一個新出口 IP                     |
| 可用性剔除   | 標記不可用、遷移 Key、每 15 分鐘複檢        | 永不剔除，完全跳過健康檢查                                |
| 匿名通道重試 | 每個出口最多存取一次，最多 `performance.max_anonymous_exits` 個出口 | 最多嘗試 `retry.max_attempts` 次，循環該池條目            |

當 URL 背後的服務本身就在做輪換、而池內出口 IP 會被單獨限流或不穩定時使用它：網關會把每次嘗試都當作新出口，而不是把整個會話固定在某個出口上。只要一條 `#pool` 條目即可，無需再羅列幾十個出口。靜態條目仍然保留下文的連接池行為。

### 超時與連接池

| 欄位                                    | 預設值 | 含義                                             |
| --------------------------------------- | ------ | ------------------------------------------------ |
| `retry.max_attempts`                    | `3`    | 每個認證 Tier 的嘗試次數，包含首次請求。         |
| `retry.timeout_seconds`                 | `300`  | 推理請求總超時，包含流式回應讀取。               |
| `performance.attempt_timeout_seconds`   | `0`    | 單次嘗試的總超時；0 表示使用請求總超時。         |
| `performance.max_anonymous_exits`       | `8`    | 每個匿名請求最多嘗試的靜態代理出口數。           |
| `performance.free_tier_block_seconds`   | `900`  | 被免費層標記的出口冷卻時長。                     |
| `performance.connect_timeout_seconds`   | `5`    | 建立連接的超時。                                 |
| `performance.failure_cooldown_seconds`  | `15`   | 失敗冷卻基數。                                   |
| `performance.max_idle_conns`            | `2048` | 每個代理 Transport 的空閒連接上限。              |
| `performance.max_idle_conns_per_host`   | `256`  | 每個 Transport 對單個主機的空閒連接上限。        |
| `performance.max_conns_per_host`        | `0`    | 單個主機的連接上限；0 表示不限制。               |
| `performance.idle_conn_timeout_seconds` | `120`  | 空閒連接保留時間。                               |

單次回應標頭超時不會超過請求總超時。希望慢節點仍能留出回退時間時，可以將它設為小於總超時的值。請求上下文過期後會停止後續嘗試，不會懲罰尚未實際使用的 Key 或代理。

### 日誌與管理端

| 欄位                          | 預設值或要求                                    |
| ----------------------------- | ----------------------------------------------- |
| `logging.level`               | `info`；可選 `debug`、`info`、`warn`、`error`。 |
| `logging.ring_size`           | `2000`；範圍為 100–50,000。                     |
| `logging.dump_request_bodies` | `false`；配合 `debug` 日誌級別輸出上游請求體。  |
| `webui.enabled`               | 省略時為 `false`；範例設定設定為 `true`。       |
| `webui.listen`                | `0.0.0.0:8081`。                                |
| `webui.username`              | 啟用 WebUI 時必填；範例為 `admin`。             |
| `webui.password`              | 首次初始化密碼，最小長度為 10。                 |
| `webui.password_hash`         | 自動生成的 Argon2id 雜湊，用於替代明文密碼。    |
| `webui.session_ttl_minutes`   | `720`；範圍為 5–10,080。                        |

啟動時會將初始密碼雜湊化，並從設定中刪除明文；包含初始明文密碼的備份也會刪除。設定和備份仍包含執行所需的上游 Key、代理憑據，應限制檔案存取權限。

請求體日誌預設關閉，每份準備好的上游請求體最多輸出 64 KiB。已設定的敏感值會脫敏，但對話內容本身仍可能包含敏感資訊。

## WebUI 與診斷

管理監聽連接埠提供 WebUI 和 `/api/*` 接口。認證使用單一管理員帳號、伺服器端 Session、HttpOnly / SameSite Cookie、寫操作 CSRF 校驗及登入限速。

| 管理接口                                         | 用途                                            |
| ------------------------------------------------ | ----------------------------------------------- |
| `POST /api/auth/login`                           | 登入，獲取 Session Cookie 和 CSRF Token。       |
| `GET /api/auth/session`、`POST /api/auth/logout` | 查看或結束目前 Session。                        |
| `GET /api/config`、`PUT /api/config`             | 讀取脫敏設定或應用修改。                        |
| `POST /api/config/reload`                        | 從磁碟重載設定。                                |
| `POST /api/config/reveal`                        | 驗證密碼後查看設定中的敏感值。                  |
| `PUT /api/account`                               | 驗證密碼後修改帳號，修改後使已有 Session 失效。 |
| `GET /api/monitor`                               | 請求、Token、上游和資源統計。                   |
| `GET /api/debug/models`                          | 模型路由、Key 指紋和 metadata 診斷。            |
| `POST /api/debug/inference`                      | 執行 Playground 請求。                          |
| `GET /api/logs`、`GET /api/logs/stream`          | 最近日誌或 SSE 即時訂閲。                       |

Playground 需要已登入的 Session 和 `X-CSRF-Token`，每個客戶端 IP 每分鐘最多 12 次：

```json
{
  "protocol": "chat",
  "key": { "mode": "auto" },
  "request": {
    "model": "MODEL_ID",
    "messages": [{ "role": "user", "content": "Hello" }]
  }
}
```

伺服器端強制設定 `stream: false`。需要指定 Key 時，使用 `{"mode":"selected","tier":"zen","id":"KEY_FINGERPRINT"}`，其中指紋來自 `/api/debug/models`。指定 Key 的診斷最多執行一次上游嘗試，不進入匿名通道，不輪換 Key，不切換 Tier，也不重放 reasoning 請求。

**自動模式和指定 Key 模式都不會改變生產 Key 的冷卻、失敗次數，以及代理的健康狀態和綁定關係。** 診斷仍會真實存取上游，可能消耗供應商配額，並會計入監控。

執行後，管理接口返回 HTTP 200，實際結果位於 `ok`、`http_status`、`request_id`、`route` 和 `response`。指定 Key 時還返回 `selected_key` 與 `key_test`，其值包括 `usable`、`rejected`、`rate_limited`、`transport_error`、`upstream_error`、`request_error`、`unavailable`。管理請求格式、認證、CSRF 和限速錯誤仍返回各自對應的 HTTP 狀態。

### 介面語言

WebUI 從 `webui/js/i18n.js` 載入翻譯，內建 **簡體中文**（預設）、**繁體中文（HK）**、**English**、**Tiếng Việt** 和 **Italiano**。原始簡體中文文案保留在 `index.html` 和 `webui/js/` 中，並始終作為缺少翻譯時的回退，因此新增語言不會覆蓋原文。

`zh-HK` 語言塊是完整的：所有鍵都譯為繁體中文並採用香港用詞（`設定`、`快取`、`帳號`、`登入`、`伺服器`、`預設`、`搜尋`），因此繁體中文（HK）不會回退到簡體原文；日期時間使用 `zh-HK` 地區設定。

- 語言選擇器位於頂部工具列右側，登入卡片上也有一個。
- 選擇結果儲存在 `localStorage`，鍵名為 `opencode2api.ui.lang`。
- 切換語言會同時重繪靜態文案（`data-i18n`、`data-i18n-attr`）和 JavaScript 生成的文案，無需刷新頁面。
- 日期時間格式跟隨所選語言。
- 瀏覽器語言為 `zh-HK`、`zh-TW` 或 `zh-MO` 時預設進入繁體中文，其他 `zh` 變體預設進入簡體中文。

新增語言時，在 `webui/js/i18n.js` 的 `DICTIONARY` 中增加一個語言塊，並在 `SUPPORTED`、`LABELS`、`FLAGS` 和 `DATE_LOCALES` 中登記其代碼，同時加入 `browserLocale` 檢測鏈。

### 儲存與重載

服務先驗證候選設定並創建新的 Gateway，然後儲存設定並切換實例。驗證或儲存失敗時，目前 Gateway 繼續處理請求；已經開始的請求繼續使用原有實例。

Key、代理、上游位址、重試、模型、日誌和路由偏好立即對新請求生效。`listen`、`webui.listen`、`webui.enabled` 的修改需要重啟進程。儲存後的 JSON 會規範化，不保留原有註釋。

## 監控與持久化

請求結果以完整推理過程為準。即使 HTTP 200 已經發出，SSE 錯誤事件或異常斷流仍會計為失敗，並標記 `stream_error`；客戶端取消標記為 `client_canceled`。WebUI 會將這些結果與 HTTP 狀態一起顯示。

上游**嘗試記錄**統計的是收到回應標頭為止的 HTTP 互動，其成功狀態和耗時與完整 JSON 回應或流的最終完成情況分開計算。

Token 統計僅使用上游報告的 usage。輸入 Token 包含快取讀取和寫入，快取 Token 單獨統計快取讀取量。缺失 usage 時不估算 Token，覆蓋率表示已經建立路由的推理請求中有多少報告了 usage。`usage.server_keys` 按呼叫方認證所用的本地 server key（指紋，與 `/api/api-keys` 的 ID 一致）拆分請求數和 Token；Playground 流量計入 `playground`。

| 數據                                            | 儲存位置或保留規則                                                        |
| ----------------------------------------------- | ------------------------------------------------------------------------- |
| Session、指標、Token 累計、最近 Playground 結果 | 進程記憶體；重啟清空。                                                      |
| 請求與嘗試明細                                  | 最近一小時，最多 10,000 個請求和 20,000 次嘗試；API 分別最多返回 500 條。 |
| 即時日誌環形緩衝區                                | 進程記憶體，大小由 `logging.ring_size` 指定。                               |
| 結構化日誌                                      | stdout JSON；需要長期保留時由外部系統收集。                               |
| 目前設定與上一版本                              | `config.json`、`config.json.bak`。                                        |
| 模型目錄快取                                    | `config.json.models.catalog.json`。                                       |
| 成本與棄用資訊快取                              | `config.json.models.dev.json`。                                           |

快取檔案名基於實際設定路徑生成。lifetime 僅表示目前進程執行期間的累計，各實例之間不共享 Session 或監控狀態。

### 健康檢查

`/healthz` 不增加監控計數，也不觸發網絡請求；返回資源數量，不包含 Key 或代理位址。

- HTTP 503：模型目錄尚未就緒、目前設定下沒有可路由模型，或沒有健康代理。
- HTTP 200：服務就緒。可用目錄快取過期時仍返回 200，同時模型狀態為 `stale`，整體狀態為 `degraded`。

過期閾值為 `models.refresh_seconds` 的兩倍，且不低於 60 秒。就緒狀態檢查目錄和資源，不會實際驗證某個上游 Key 能否完成下一次推理。

## 開發

Go 原始碼按職責拆分為獨立包。程式入口負責組裝服務，具體實現放在 `internal/` 中。

```text
cmd/
  opencode2api/main.go    命令行參數、啟動與優雅退出
internal/
  admin/                 管理 API、登入會話與 Playground
  buildinfo/             健康檢查和管理接口共享的版本資訊
  config/                設定解析、持久化、密碼與脫敏
  gateway/               HTTP 路由、重試、資源池、刷新與執行時
  httpx/                 通用 HTTP 回應、回應體處理與請求標頭
  identity/              請求標識與會話親和性
  jsonutil/              JSON 取值與解碼輔助函數
  models/                模型目錄、能力、價格與快取
  protocol/              請求和回應轉換、SSE 解析與輸出
  telemetry/             請求跟蹤、指標、日誌與異常恢復
webui/
  embed.go               將靜態資源內嵌到可執行檔案
  index.html             頁面結構
  css/                   設計 token、基礎樣式、組件與頁面佈局
  js/                    ES 模組：外殼、i18n、共享輔助與每頁一個模組
```

建議閲讀順序：

1. [程式入口](cmd/opencode2api/main.go) → [執行時管理](internal/gateway/runtime.go) → [HTTP 處理](internal/gateway/gateway.go)。
2. [上游請求](internal/gateway/upstream.go)和[模型路由](internal/models/catalog.go)說明請求如何選擇併到達上游。
3. [請求轉換](internal/protocol/request.go)、[回應轉換](internal/protocol/response.go)和[流式傳輸](internal/protocol/stream.go)說明協議處理過程。
4. [管理路由](internal/admin/server.go)和 [WebUI 互動](webui/js/main.js)說明設定編輯與診斷功能。

執行 Go 格式化、靜態分析和程式構建：

```bash
gofmt -w cmd internal webui
go vet ./...
go build -o opencode2api ./cmd/opencode2api
```

開發時可以直接執行 `go run ./cmd/opencode2api -config config.json`。發佈構建仍透過 `-ldflags "-X main.version=vX.Y.Z"` 注入版本號。

Node.js 僅用於 WebUI 的 JavaScript 語法檢查：

```bash
for f in webui/js/*.js webui/js/pages/*.js; do node --input-type=module --check < "$f"; done
```

項目透過 `.editorconfig`、`.gitattributes` 和 Go 格式化統一格式。CI 覆蓋 Linux / Windows 上的 Go 1.24 與穩定版 Go 的 `go vet` 和構建，以及 Go 格式、WebUI 語法和容器入口腳本語法檢查。發佈壓縮包包含五份 README。

## 常見問題

| 現象                          | 排查方向                                           |
| ----------------------------- | -------------------------------------------------- |
| API 返回 401                  | 使用設定中的本地 `server_keys`。                   |
| 健康檢查一直為 `starting`     | 查看目錄刷新日誌和網絡連通性，必要時補充協議覆蓋。 |
| 模型列表為空                  | 檢查設定的 Tier、匿名資格和原生協議是否受支援。    |
| 請求返回 502 / 504            | 查看上游嘗試、憑據、代理以及請求總超時和單次超時。 |
| HTTP 200 但生成失敗           | 查看 SSE 錯誤事件和請求結果，不能只看 HTTP 狀態。  |
| Docker 中宿主機設定修改不生效 | 目前設定在狀態卷中；重新導入，或透過 WebUI 修改。  |
| 無法透過容器連接埠存取          | 檢查監聽位址、連接埠映射和狀態卷中的實際設定。       |
| 重啟後監控消失                | 監控僅儲存在記憶體，需要外部收集 stdout 日誌。       |

## 自適應思考強度

將 `reasoning.effort` 或按模型覆蓋值設定為 `auto`，即可使用免費模型探測實際驗證過的最高可用檔。已有具體檔位語義不變，客戶端顯式指定的強度始終優先，不維護模型名稱白名單。

基礎請求成功後，對能力目錄標記支援 reasoning 的模型依次驗證 `max`、`xhigh`、`high`、`medium`、`low`、`minimal`。只有明確的思考參數不支援錯誤才嘗試下一檔；429、超時等臨時失敗會停止搜尋，不能據此判斷模型檔位上限。全部檔位均被拒絕時，不注入思考覆蓋。

結果按上游 URL、模型與原生協議持久化，在可用性頁展示，最多使用兩小時內的驗證結果。主動探測仍只覆蓋免費 Zen/匿名模型；Go/付費模型、能力未知或尚未驗證的模型不猜測檔位。開啟 `auto` 不擴大探測範圍，也不能證明上游一定執行了所有接受的欄位；每個模型可能增加數次簡短請求並消耗上游額度。

## 致謝

本儲存庫是 [jasonxu114514](https://github.com/jasonxu114514) 的 [opencode2api](https://github.com/jasonxu114514/opencode2api) 的分支。網關設計、協議轉換、WARP 出口、模型輪轉以及本文檔的大部分內容均來自原專案，原創成果歸原作者所有；本分支的本地修改在同一 GPL-3.0-or-later 許可下單獨維護。

## 許可證

SPDX-License-Identifier: GPL-3.0-or-later

本項目是自由軟件，採用自由軟件基金會發布的 GNU 通用公共許可證第 3 版或（由你選擇的）任何後續版本。你可以按該許可證重新分發和修改本項目；本項目不提供任何擔保。完整條款見 [LICENSE](LICENSE)。
