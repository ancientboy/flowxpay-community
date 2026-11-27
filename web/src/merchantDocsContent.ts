import contract from "./merchant-openapi.json";

export const merchantContract = contract;
export type Text = readonly [en: string, zh: string];
const paymentID = "ININDEMO123456789";
const refundID = "RFINDEMO123456789";
const payment = {
  id: paymentID, merchant_reference: "DEMO-IN-001", market: "IN", currency: "INR", method: "gateway",
  amount_minor: 24900, status: "requires_action", checkout_url: "/sandbox/checkout/EXAMPLE",
  next_action: { type: "redirect", url: "/sandbox/checkout/EXAMPLE/pay" },
  created_at: "2026-01-01T12:00:00Z", expires_at: "2026-01-01T12:15:00Z",
};
const refund = {
  id: refundID, payment_id: paymentID, currency: "INR", amount_minor: 1000,
  status: "pending_confirmation", created_at: "2026-01-01T12:30:00Z", updated_at: "2026-01-01T12:30:00Z",
};
export const eventSample = {
  id: "evt_demo_succeeded", type: "payment.succeeded", payment_id: paymentID, status: "succeeded",
  currency: "INR", amount_minor: 24900, occurred_at: "2026-01-01T12:10:00Z",
};
export const json = (value: unknown) => JSON.stringify(value, null, 2);

type Endpoint = {
  id: string; title: Text; note: Text; method: "get" | "post"; path: keyof typeof contract.paths;
  uri: string; output: unknown; body?: object; idempotency?: string;
};
export const endpoints: Endpoint[] = [
  { id: "capabilities", title: ["List capabilities", "查询可用支付能力"],
    note: ["Read the enabled market, currency and method combinations for your merchant and sites.",
      "查询当前商户及站点可用的市场、币种和支付方式组合。"],
    method: "get", path: "/capabilities", uri: "/capabilities", output: {
      environment: "sandbox", capability_version: "sandbox-collections-v1",
      merchant: { id: "M10028", name: "Demo Merchant" },
      payments: [{ market: "IN", currency: "INR", method: "gateway" }],
      sites: [{ id: "DEMO-SITE", name: "Demo Site", default: true, accounts: [], payments: [{ market: "IN", currency: "INR", method: "gateway" }] }],
    } },
  { id: "create-payment", title: ["Create a payment", "创建代收订单"],
    note: ["Use a capability from the previous call. If the status is requires_action, open checkout_url on the sandbox web origin or follow next_action. A checkout return does not confirm payment.",
      "使用能力查询返回的组合。状态为 requires_action 时，打开沙盒网页域名下的 checkout_url，或按 next_action 操作。收银台跳回后应通过查单或已验签通知确认支付结果。"],
    method: "post", path: "/payments", uri: "/payments", idempotency: "DEMO-PAYMENT-001",
    body: contract.paths["/payments"].post.requestBody.content["application/json"].examples.indiaGateway.value,
    output: { ...payment, merchant_reference: "IN-DEMO-01" } },
  { id: "list-payments", title: ["List or look up payments", "列出或按商户单号查找代收"],
    note: ["After a timeout, query the original merchant_reference. To retry creation, keep the original payload and idempotency key.",
      "超时后先查询原 merchant_reference。需要重试创建时，保留原请求内容和原幂等键。"],
    method: "get", path: "/payments", uri: "/payments?merchant_reference=DEMO-IN-001", output: { data: [payment] } },
  { id: "get-payment", title: ["Get a payment", "查询代收订单详情"],
    note: ["Use the FlowXPay order ID. Treat pending_confirmation as unresolved. Only succeeded confirms payment.",
      "使用 FlowXPay 订单号。pending_confirmation 表示结果待确认，succeeded 表示付款成功。"],
    method: "get", path: "/payments/{id}", uri: "/payments/" + paymentID, output: payment },
  { id: "create-refund", title: ["Create a refund", "发起退款"],
    note: ["Refund a succeeded payment using a separate idempotency key. Use the refund ID to check its final status.",
      "仅已支付成功的订单可退款。退款使用独立幂等键，并用返回的退款 ID 查询最终状态。"],
    method: "post", path: "/payments/{id}/refunds", uri: "/payments/" + paymentID + "/refunds",
    idempotency: "DEMO-REFUND-001", body: { amount_minor: 1000 }, output: refund },
  { id: "list-refunds", title: ["List refunds for a payment", "列出原单的退款"],
    note: ["Each refund has its own amount and status.", "每笔退款分别记录金额和状态。"],
    method: "get", path: "/payments/{id}/refunds", uri: "/payments/" + paymentID + "/refunds", output: { data: [refund] } },
  { id: "get-refund", title: ["Get a refund", "查询退款详情"],
    note: ["Check for succeeded, failed or pending_confirmation.", "通过 succeeded、failed 或 pending_confirmation 判断退款进度。"],
    method: "get", path: "/refunds/{id}", uri: "/refunds/" + refundID, output: refund },
  { id: "exceptions", title: ["List collection exceptions", "查询代收异常"],
    note: ["Review unresolved confirmations, conflicting updates and failed Webhook deliveries.",
      "查看待确认订单、状态冲突和 Webhook 投递失败。"],
    method: "get", path: "/collection-exceptions", uri: "/collection-exceptions", output: {
      data: [{ kind: "payment_confirmation_pending", payment_id: paymentID, subject_id: paymentID, at: "2026-01-01T12:05:00Z" }],
    } },
];

export const languages = ["cURL", "Node.js", "Python"] as const;
export type SampleLanguage = typeof languages[number];

export function requestExample(endpoint: Endpoint, language: SampleLanguage) {
  const uri = "/v1" + endpoint.uri;
  const body = endpoint.body && JSON.stringify(endpoint.body);
  if (language === "cURL") {
    const lines = ['curl -sS -i "${FLOWXPAY_BASE_URL}' + uri + '"', '  -H "Authorization: Bearer ${FLOWXPAY_API_KEY}"'];
    if (body) lines.push("  -H 'Content-Type: application/json'",
      `  -H 'Idempotency-Key: ${endpoint.idempotency}'`, `  --data '${body}'`);
    return lines.join(" \\" + "\n");
  }
  if (language === "Node.js") {
    return [
      'const base = process.env.FLOWXPAY_BASE_URL;', 'const key = process.env.FLOWXPAY_API_KEY;',
      `const response = await fetch(base + "${uri}", {`,
      `  method: "${endpoint.method.toUpperCase()}",`, '  headers: { Authorization: `Bearer ${key}`,',
      ...(body ? ['    "Content-Type": "application/json",', `    "Idempotency-Key": "${endpoint.idempotency}",`] : []),
      '  },', ...(body ? [`  body: JSON.stringify(${body}),`] : []),
      '});', 'console.log(response.status, await response.json());',
    ].join("\n");
  }
  return [
    'import json, os', 'from urllib.request import Request, urlopen', 'from urllib.error import HTTPError', '',
    'base = os.environ["FLOWXPAY_BASE_URL"]', 'key = os.environ["FLOWXPAY_API_KEY"]',
    'headers = {"Authorization": f"Bearer {key}"}',
    ...(body ? [`headers.update({"Content-Type": "application/json", "Idempotency-Key": "${endpoint.idempotency}"})`] : []),
    `request = Request(base + "${uri}", headers=headers${body ? `, data=json.dumps(${body}).encode("utf-8")` : ""})`,
    'try:', '    with urlopen(request, timeout=10) as response:', '        print(response.status, json.load(response))',
    'except HTTPError as error:', '    print(error.code, json.load(error))',
  ].join("\n");
}

export const webhookPython = String.raw`import hashlib, hmac, json, time

def verify_webhook(raw_body, headers, merchant_id, signing_key):
    # Retrieve exactly one value for each header before calling this function.
    timestamp = headers["FlowXPay-Timestamp"]
    signature = headers["FlowXPay-Signature"]
    if not timestamp.isascii() or not timestamp.isdecimal():
        raise ValueError("Invalid timestamp")
    seconds = int(timestamp)
    if str(seconds) != timestamp or abs(int(time.time()) - seconds) > 300:
        raise ValueError("Expired or invalid timestamp")
    prefix = f"v1\n{timestamp}\n{merchant_id}\n".encode("utf-8")
    expected = hmac.new(signing_key, prefix + raw_body, hashlib.sha256).digest()
    if len(signature) != 64 or not hmac.compare_digest(expected, bytes.fromhex(signature)):
        raise ValueError("Bad signature")
    event = json.loads(raw_body)
    if event["id"] != headers["FlowXPay-Event-ID"]:
        raise ValueError("Event ID mismatch")
    return event

# Store the event ID and apply the verified update atomically before returning 2xx.`;

export const errors: Record<string, { note: Text; code: string; message: string }> = {
  "400": { note: ["Check missing or invalid parameters and headers.", "检查必填字段、参数值和请求头。"],
    code: "invalid_idempotency_key", message: "Supply one valid Idempotency-Key." },
  "401": { note: ["Check the API key and environment.", "检查 API 密钥及所用环境。"],
    code: "unauthorized", message: "A valid sandbox API key is required." },
  "404": { note: ["Check the order ID, site or assigned payment capability.", "检查订单号、站点及可用支付能力。"],
    code: "payment_not_found", message: "Payment not found." },
  "409": { note: ["Check conflicting references, idempotency input or refundable balance.", "核对冲突的单号、幂等请求内容或可退金额。"],
    code: "idempotency_conflict", message: "The key is already assigned to a different request." },
  "415": { note: ["Use Content-Type: application/json.", "使用 Content-Type: application/json。"],
    code: "invalid_content_type", message: "Send application/json." },
  "429": { note: ["Honor Retry-After. Retry with the same key and payload.", "按 Retry-After 等待，重试沿用原幂等键与请求内容。"],
    code: "channel_rate_limited", message: "Retry with the original Idempotency-Key." },
  "503": { note: ["Look up an uncertain order before retrying with the original key.", "结果不确定时先查原单，重试时保留原幂等键。"],
    code: "database_unavailable", message: "Sandbox key authentication is unavailable." },
};

export const signedRequestNode = String.raw`import { createHash, randomBytes, sign } from "node:crypto";
import { readFileSync } from "node:fs";

const method = "POST";
const target = "/v1/payments";
const contentType = "application/json";
const body = JSON.stringify({
  merchant_reference: "SIGNED-ORDER-001", market: "US",
  currency: "USD", method: "card", amount_minor: 4900
});
const idempotencyKey = "SIGNED-ORDER-001"; // Keep this value on retries.
const timestamp = String(Math.floor(Date.now() / 1000));
const nonce = randomBytes(24).toString("base64url"); // Generate again on each retry.
const merchantId = process.env.FLOWXPAY_MERCHANT_ID;
const keyId = process.env.FLOWXPAY_SIGNING_KEY_ID;
const privateKey = readFileSync(process.env.FLOWXPAY_SIGNING_KEY_FILE);
const bodyHash = createHash("sha256").update(body, "utf8").digest("hex");
const signingInput = ["flowxpay-request-v1", "sandbox", merchantId, keyId,
  method, target, contentType, bodyHash, idempotencyKey, timestamp, nonce].join("\n");
const signature = sign(null, Buffer.from(signingInput, "utf8"), privateKey).toString("base64url");
const response = await fetch(process.env.FLOWXPAY_BASE_URL + target, {
  method, body, redirect: "error",
  headers: {
    "Authorization": "Bearer " + process.env.FLOWXPAY_API_KEY,
    "Content-Type": contentType,
    "Idempotency-Key": idempotencyKey,
    "FlowXPay-Key-ID": keyId,
    "FlowXPay-Request-Timestamp": timestamp,
    "FlowXPay-Request-Nonce": nonce,
    "FlowXPay-Request-Signature": signature
  }
});
console.log(response.status, await response.json());`;
