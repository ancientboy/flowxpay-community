# FlowXPay merchant API integration guide

English · [简体中文](merchant-api.md) · [Interactive API reference](https://ancientboy.github.io/flowxpay-community/?workspace=merchant&page=api-docs&lang=en-US)

Use this guide to integrate with the FlowXPay Community sandbox. Orders, refunds and callbacks use simulated data. See the [OpenAPI specification](openapi.yaml) for fields, statuses and response schemas.

## 1. Get a sandbox key and check available payment methods

Get a sandbox key from **API keys** in the merchant workspace. Send it from your server in the `Authorization: Bearer <API_KEY>` header. FlowXPay identifies your merchant from the key. You can omit the merchant number and MD5 signature. Merchants enrolled in per-request signing also sign payment creation and refunds with Ed25519, as described in section 4. Keep the key in a server-side secret store or environment variable. Keep it out of URLs, browser code and logs. Use HTTPS over the network. For local development, an HTTP server bound to loopback is sufficient.

Set `FLOWXPAY_BASE_URL` to the sandbox API origin supplied by the platform, omitting `/v1`. The examples read the key from the environment.

```sh
curl -sS "${FLOWXPAY_BASE_URL}/v1/capabilities" \
  -H "Authorization: Bearer ${FLOWXPAY_API_KEY}"
```

Read the allowed market, currency and method combinations from `payments` or the relevant entry in `sites`. The following are sandbox examples. Use the combinations returned for your merchant.

| Market | Currency | `method` |
| --- | --- | --- |
| United States `US` | `USD` | `card` |
| Brazil `BR` | `BRL` | `pix` |
| India `IN` | `INR` | `gateway` |

## 2. Create a payment and open checkout

Send amounts as integers in the currency’s minor unit. For example, `amount_minor: 24900` means INR 249.00. Store a unique `merchant_reference` and `Idempotency-Key` for each order. To use a non-default site, pass the `site_id` returned by the capabilities endpoint.

```sh
curl -sS -X POST "${FLOWXPAY_BASE_URL}/v1/payments" \
  -H "Authorization: Bearer ${FLOWXPAY_API_KEY}" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: IN-DEMO-ORDER-001' \
  --data '{"merchant_reference":"IN-DEMO-001","market":"IN","currency":"INR","method":"gateway","amount_minor":24900}'
```

Store the returned `id`. When the order has status `requires_action`, resolve its relative `checkout_url` against the **sandbox checkout web origin** supplied by the platform and direct the customer there. Custom frontends can handle `redirect` or `display_qr` according to `next_action.type`. If no action is available, look up the original order. Confirm the final payment status from your server before fulfilling or crediting an order, even if the customer has already returned from checkout.

| Operation | API |
| --- | --- |
| Find a payment by platform ID | `GET /v1/payments/{id}` |
| Find a payment by merchant reference | `GET /v1/payments?merchant_reference=IN-DEMO-001` |
| Create a refund | `POST /v1/payments/{id}/refunds` with `amount_minor` and the refund’s own idempotency key |
| Look up a refund | `GET /v1/refunds/{id}` |

A successful creation returns `201`. Replaying the same request with its original idempotency key returns the original order and `200`. After a timeout, look up the order by its original merchant reference. While the result is unknown, retain the original payload and key. Reuse both if a retry is needed. For `pending_confirmation`, keep checking the original order.

| Response | Action |
| --- | --- |
| `400` or `415` | Check request fields, idempotency key and Content-Type. |
| `401` | Verify the sandbox environment and check whether the key is valid or revoked. |
| `409` | Compare the original order, idempotency key and payload. |
| `429` | Wait for the interval in `Retry-After` and reuse the original key. |
| `503` or timeout | Look up the original order. Reuse its key if a retry is needed. |

Workspace members with the appropriate permissions can create, approve and look up sandbox payout drafts. The `/v1/payouts` endpoint requires a workspace member session. A merchant API key cannot call it. Settlement and reconciliation are available in the sandbox workspace. The community sandbox does not offer real-money operations or server-to-server merchant APIs for payouts and settlements.

## 3. Verify sandbox webhooks

The sandbox delivers webhooks to a locally configured receiver using a literal loopback IP. Each merchant has a webhook signing key separate from its Bearer key. Configure a random test root key in `FLOWXPAY_SANDBOX_WEBHOOK_ROOT` and a receiver address for the merchant, for example `FLOWXPAY_SANDBOX_WEBHOOK_M10028`. Derive the merchant’s key by applying HMAC-SHA256 with the root key to `flowxpay-sandbox-v1\nwebhook\n<merchant-id>`.

1. Capture the raw request body and read exactly one value for each of `FlowXPay-Timestamp`, `FlowXPay-Signature` and `FlowXPay-Event-ID`.
2. Accept UTC Unix timestamps within 300 seconds of the current time. Encode `v1\n<timestamp>\n<merchant_id>\n` as UTF-8 and append the original body bytes. Each `\n` represents one LF byte. Compute HMAC-SHA256 with the webhook signing key and compare the hex signature in constant time. Preserve the original JSON bytes until verification completes.
3. Verify that the event ID in the header matches `id` in the signed JSON. Persistently deduplicate by event ID and acknowledge with HTTP 2xx after committing the business state. Deliveries may repeat or arrive out of order. Verify the order, currency and amount before acting on a terminal state. Look up the original order if states conflict.

Use this fixed test vector to check the byte sequence and signature calculation. Use a test clock for the timestamp window.

| Input | Value |
| --- | --- |
| Signing key, hex | `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f` |
| Merchant ID | `M10028` |
| Unix timestamp | `1700000000` |
| Raw body without a trailing newline | `{"id":"evt_demo"}` |
| Expected signature | `7633e1259cf76158fe581bfde873ecf73527f1413ab0cd50cb7abaa79cf70558` |

Decode the hex key to 32 bytes before computing the HMAC. The vector tests only the signature. Validate actual events against the [OpenAPI event schema](openapi.yaml).

## 4. Enable per-request signing for a sandbox merchant

Operators register public keys by merchant ID in the sandbox service environment variable `FLOWXPAY_SANDBOX_REQUEST_PUBLIC_KEYS`. Use the form `{"M10028":{"key-2026":"<unpadded Base64URL of the raw 32-byte Ed25519 public key>"}}`. Register overlapping key IDs during rotation. Keep private keys on the merchant server. Enrolled merchants use Bearer authentication and a signature for payment creation and refunds. Read endpoints continue to use Bearer authentication. Sandbox payouts use a workspace member session.

Send one `Content-Type: application/json` header. Sign the following 11 lines as UTF-8, separated by LF, with no trailing newline.

```text
flowxpay-request-v1
sandbox
<merchant_id>
<FlowXPay-Key-ID>
<HTTP method>
<raw path and query string>
<Content-Type>
<lowercase hex SHA-256 of the raw request body>
<Idempotency-Key>
<FlowXPay-Request-Timestamp>
<FlowXPay-Request-Nonce>
```

Encode the binary Ed25519 signature as unpadded Base64URL in `FlowXPay-Request-Signature`. The timestamp is UTC Unix time in seconds, with a ±300-second window. Generate the nonce from at least 16 random bytes and encode it as unpadded Base64URL. After a timeout, look up the order first. If retrying, reuse the original idempotency key and body, and generate a fresh timestamp and nonce. Reusing a nonce returns HTTP 409. An invalid signature or expired timestamp returns HTTP 401. The interactive merchant API reference includes a Node.js signing example. Production transactions depend on merchant permissions, channel routing and available funds.

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
