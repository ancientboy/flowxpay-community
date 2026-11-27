# FlowXPay 商户 API 接入指南

本指南适用于 FlowXPay 社区版沙盒。这里的订单、退款和回调均使用模拟数据。接口字段、状态及响应结构见 [OpenAPI 规范](openapi.yaml)。

## 1. 获取沙盒密钥并查询可用通道

从商户后台的「API 密钥」获取沙盒 API Key。由商户服务端发送 `Authorization: Bearer <API_KEY>`，FlowXPay 根据密钥识别商户。请求不需要传商户号或计算 MD5。已开通逐笔签名的沙盒商户必须在创建代收和退款时提供 Ed25519 签名，详见下方。密钥只应保存在服务端的密钥管理系统或环境变量中，调用时放在请求头，避免写入 URL、浏览器代码和日志。通过网络访问时使用 HTTPS。本地开发可使用仅监听回环地址的 HTTP 服务。

将 `FLOWXPAY_BASE_URL` 设为平台提供的沙盒 API 根地址，不包含 `/v1`。以下示例从环境变量读取密钥。

```sh
curl -sS "${FLOWXPAY_BASE_URL}/v1/capabilities" \
  -H "Authorization: Bearer ${FLOWXPAY_API_KEY}"
```

从返回的 `payments` 或对应 `sites` 中读取允许的市场、币种和方式。使用同一份 API 按能力创建订单。下表是沙盒示例，以商户实际返回的能力为准。

| 市场 | 币种 | `method` |
| --- | --- | --- |
| 美国 `US` | `USD` | `card` |
| 巴西 `BR` | `BRL` | `pix` |
| 印度 `IN` | `INR` | `gateway` |

## 2. 创建代收订单并打开收银台

金额使用最小货币单位的整数。例如 `amount_minor: 24900` 表示 INR 249.00。每笔业务订单保存唯一的 `merchant_reference` 和 `Idempotency-Key`。使用非默认站点时，按 OpenAPI 传入从能力查询获取的 `site_id`。

```sh
curl -sS -X POST "${FLOWXPAY_BASE_URL}/v1/payments" \
  -H "Authorization: Bearer ${FLOWXPAY_API_KEY}" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: IN-DEMO-ORDER-001' \
  --data '{"merchant_reference":"IN-DEMO-001","market":"IN","currency":"INR","method":"gateway","amount_minor":24900}'
```

保存返回的 `id`。当 `status` 是 `requires_action` 时，可将返回的相对路径 `checkout_url` 拼在平台提供的**沙盒收银台网页域名**后交给用户。自建前端按 `next_action.type` 处理 `redirect` 或 `display_qr`。没有可用动作时查询原单。用户返回页面或打开收银台只代表页面操作，发货或入账前应由服务端确认最终订单状态。

| 操作 | API |
| --- | --- |
| 按平台订单号查单 | `GET /v1/payments/{id}` |
| 按原商户订单号查单 | `GET /v1/payments?merchant_reference=IN-DEMO-001` |
| 发起退款 | `POST /v1/payments/{id}/refunds`，传 `amount_minor` 和退款自己的幂等键 |
| 查询退款 | `GET /v1/refunds/{id}` |

首次创建返回 `201`。同一请求使用原幂等键重试时返回原订单和 `200`。遇到网络超时先按原商户订单号查单。结果未知时，保留请求内容和原幂等键。需要重试时使用同一请求和同一键。出现 `pending_confirmation` 时继续查原单。以下是常见错误处理。

| 返回 | 处理 |
| --- | --- |
| `400` 或 `415` | 检查请求字段、幂等键和 Content-Type |
| `401` | 检查沙盒环境以及密钥是否有效或已撤销 |
| `409` | 核对原业务订单、幂等键与请求内容 |
| `429` | 按响应中的 `Retry-After` 等待，重试保留原幂等键 |
| `503` 或网络超时 | 查原单，确认需要重试时沿用原幂等键 |

代付当前通过商户后台成员权限创建、审批和查询沙盒草稿。`/v1/payouts` 需要工作台成员会话，商户 API Key 无法调用。结算和对账目前通过沙盒后台查看。商户服务端代付、结算 API 与真实资金操作尚未开放。

## 3. 验证 FlowXPay Webhook

当前仅支持平台在本地配置的回环 Webhook 接收地址。沙盒接收端使用**独立的每商户 Webhook 签名密钥**，与 Bearer API Key 分开。配置随机测试根密钥 `FLOWXPAY_SANDBOX_WEBHOOK_ROOT` 和商户接收地址（例如 `FLOWXPAY_SANDBOX_WEBHOOK_M10028`）。地址使用字面回环 IP。每商户签名密钥由根密钥对 `flowxpay-sandbox-v1\nwebhook\n<merchant-id>` 做 HMAC-SHA256 派生。

1. 获取原始请求体字节，读取 `FlowXPay-Timestamp`、`FlowXPay-Signature` 和 `FlowXPay-Event-ID`，每个头都应只有一个值。
2. 时间戳为 UTC Unix 秒，仅接受与当前时间相差不超过 300 秒的事件。将 `v1\n<timestamp>\n<merchant_id>\n` 编为 UTF-8 后拼接原始请求体字节。每个 `\n` 表示一个换行字节。使用 Webhook 签名密钥计算 HMAC-SHA256，并用恒定时间比较校验十六进制签名。校验前保留 JSON 字节原样。
3. 校验签名后，确认请求头的事件 ID 等于已签名 JSON 里的 `id`。按事件 ID 持久去重，并在业务状态写入成功后回复 2xx。投递可能重试，也可能乱序。处理终态前核对订单、币种和金额。状态冲突时查询原订单。

**验签测试向量**用于核对字节拼接和 HMAC 实现。时间戳为历史固定值，通过接收端时间窗口检查时应使用测试时钟。

| 项目 | 值 |
| --- | --- |
| 签名密钥的十六进制编码 | `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f` |
| 商户标识 | `M10028` |
| 时间戳 | `1700000000` |
| 原始请求体，无结尾换行 | `{"id":"evt_demo"}` |
| 预期签名 | `7633e1259cf76158fe581bfde873ecf73527f1413ab0cd50cb7abaa79cf70558` |

计算 HMAC 时先把十六进制密钥解码成 32 字节。测试向量只验证签名，实际事件还须通过 [OpenAPI 中的事件结构](openapi.yaml) 校验。


## 4. 为沙盒商户启用逐笔请求签名

运营通过沙盒服务环境变量 `FLOWXPAY_SANDBOX_REQUEST_PUBLIC_KEYS` 按商户 ID 登记公钥，格式为 `{"M10028":{"key-2026":"<32 字节原始 Ed25519 公钥的无填充 Base64URL>"}}`。允许同一商户在迁移时同时登记不同公钥及 ID。私钥只在商户服务端保存。登记后，该商户的创建代收与退款接口要求 Bearer 认证和签名。读取类接口继续使用 Bearer 认证。沙盒代付目前仍走工作台成员会话。

始终发送单个 `Content-Type: application/json`，签名使用以下 11 行的 UTF-8 文本，每行用 LF 分隔，末尾没有额外换行。

```text
flowxpay-request-v1
sandbox
<merchant_id>
<FlowXPay-Key-ID>
<HTTP method>
<原始路径和查询串>
<Content-Type>
<原始请求体的 SHA-256 小写十六进制>
<Idempotency-Key>
<FlowXPay-Request-Timestamp>
<FlowXPay-Request-Nonce>
```

将二进制 Ed25519 签名编码为无填充 Base64URL，设置 `FlowXPay-Request-Signature`。时间戳为 UTC Unix 秒，窗口 ±300 秒。nonce 建议使用至少 16 个随机字节的无填充 Base64URL。网络超时先查原单，重试时使用原幂等键、同一请求体、新时间戳和新 nonce。重复 nonce 返回 HTTP 409；错误签名或超时签名返回 HTTP 401。商户端 API 文档包含 Node.js 签名示例。正式交易取决于商户权限、通道路由和资金可用状态。

### Production transaction notifications

A merchant administrator configures a public HTTPS endpoint on port 443 in **API keys → Merchant transaction notifications**. Configure or rotate displays a one-time base64url secret. Store it on your server. Endpoint setup is independent of the merchant API request-signing credential.

FlowXPay sends JSON via POST for collection or payout terminal transitions. Fields are `event_id`, `type` (e.g. `collection.succeeded`), `order_id`, `merchant_id`, `merchant_reference`, `status`, `currency`, and `amount_minor` (string). To verify, base64url-decode the key and calculate lowercase hexadecimal HMAC-SHA256 over the exact bytes `timestamp + "." + event_id + "." + raw_body`. Verify the signature using constant-time comparison, check that the timestamp is within five minutes of your server clock, and that header and body event IDs and merchant identity agree. Do not parse and reserialize the body before verification.

Python example (header names are case-insensitive):

```python
import base64, hashlib, hmac, json, time

def verify_event(headers, raw, secret, merchant):
    ts = headers["X-FlowXPay-Timestamp"]
    event_id = headers["X-FlowXPay-Event"]
    key = base64.urlsafe_b64decode(secret + "=" * (-len(secret) % 4))
    expected = hmac.new(key, f"{ts}.{event_id}.".encode() + raw, hashlib.sha256).hexdigest()
    if abs(time.time() - int(ts)) > 300 or not hmac.compare_digest(expected, headers["X-FlowXPay-Signature"]):
        raise ValueError("Invalid signature or expired timestamp")
    event = json.loads(raw)
    if event["event_id"] != event_id or event["merchant_id"] != merchant:
        raise ValueError("Event identity mismatch")
    return event
```

Commit the event ID under a unique constraint in the same transaction as your order update. Return HTTP 2xx after commit, including for an already committed delivery. Redirects and other responses do not acknowledge delivery. Retry delays are 10 seconds, 30 seconds, 2 minutes, 10 minutes, 30 minutes, 2 hours, 6 hours and 12 hours. A crash can cause repeat deliveries. An event recorded before configuration, or exhausted after retries, can be manually redelivered under its original event ID. Reconfiguration never silently redirects previously queued events to a different URL.
