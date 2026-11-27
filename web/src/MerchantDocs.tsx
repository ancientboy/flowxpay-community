import { useState } from "react";
import { DownloadSimple } from "@phosphor-icons/react";
import { MerchantDocCode as Code } from "./MerchantDocCode";
import { useLocale } from "./locale";
import { endpoints, errors, eventSample, json, languages, merchantContract, requestExample, signedRequestNode, webhookPython,
  type SampleLanguage, type Text } from "./merchantDocsContent";

type Schema = {
  $ref?: string; type?: string; description?: string; properties?: Record<string, Schema | undefined>; required?: string[];
  allOf?: Schema[]; anyOf?: Schema[]; oneOf?: Schema[]; items?: Schema; const?: string; enum?: (string | number)[]; pattern?: string; minimum?: number; maximum?: number;
};
type Parameter = { name: string; in: string; required?: boolean; description?: string; schema: Schema };
type Reply = { $ref?: string; description?: string; content?: { "application/json": { schema: Schema } } };
type Operation = { parameters?: Parameter[]; requestBody?: Reply; responses: Record<string, Reply> };
type Route = { parameters?: Parameter[]; get?: Operation; post?: Operation };
const schemas: Record<string, Schema> = merchantContract.components.schemas;
const choose = (value: Text, zh: boolean) => value[zh ? 1 : 0];

const fieldNotes: Record<string, Text> = {
  "Idempotency-Key": ["Reuse for the same operation and input.", "同一操作、同一参数重试时须复用。"],
  "Content-Type": ["Send application/json.", "使用 application/json。"],
  id: ["ID returned by FlowXPay.", "FlowXPay 返回的订单或退款 ID。"],
  merchant_reference: ["Your exact order reference.", "商户的原始订单号，按完整值匹配。"],
  site_id: ["Omit to use your default site.", "省略时使用默认站点。"],
  amount_minor: ["Integer in minor units, e.g. 24900 = INR 249.00.", "最小货币单位的整数，例如 24900 = INR 249.00。"],
  market: ["Read from capabilities.", "从可用支付能力获取。"],
  currency: ["Match the market and payment method.", "与市场及支付方式组合匹配。"],
  method: ["Match an enabled capability.", "使用已开通的方式。"],
  "FlowXPay-Key-ID": ["Registered Ed25519 key ID.", "已登记的 Ed25519 密钥 ID。"],
  "FlowXPay-Request-Timestamp": ["UTC Unix seconds, within 300 seconds.", "UTC Unix 秒，允许偏差 300 秒。"],
  "FlowXPay-Request-Nonce": ["Unique, unpadded Base64URL. Generate a fresh random value for each request.", "每次请求生成不重复的随机值，使用无填充的 Base64URL。"],
  "FlowXPay-Request-Signature": ["Ed25519 signature, unpadded Base64URL.", "Ed25519 签名，使用无填充的 Base64URL。"],
  "FlowXPay-Event-ID": ["Stable ID, also present in the signed JSON.", "稳定事件编号，必须与已签名 JSON 中的 id 一致。"],
  "FlowXPay-Timestamp": ["UTC Unix seconds. The tolerance is 300 seconds.", "UTC Unix 秒，与当前时间相差不能超过 300 秒。"],
  "FlowXPay-Signature": ["Hex-encoded HMAC-SHA256.", "HMAC-SHA256 的十六进制编码。"],
};
const intro: Text = [
  "Use your sandbox key in the Authorization: Bearer header from your server. Set FLOWXPAY_BASE_URL to the sandbox API origin without /v1. Example keys and IDs are placeholders.",
  "在服务端通过 Authorization: Bearer 请求头传入沙盒密钥。将 FLOWXPAY_BASE_URL 设为沙盒 API 根地址，不含 /v1。示例中的密钥及编号均为占位值。",
];
const coverage: Text = [
  "This reference covers the sandbox merchant API. Payouts and settlements are available in the workspace.",
  "本页适用于沙盒商户 API。代付和结算请通过商户后台操作。",
];
const signing: Text = [
  "Sandbox writes use a Bearer key and idempotency. For merchants enrolled in sandbox request signing, payment creation and refunds also require an Ed25519 signature. Webhooks use a separate HMAC key.",
  "沙盒写操作使用 Bearer 密钥和幂等键。已开通沙盒逐笔签名的商户，创建代收和退款还须提交 Ed25519 签名。Webhook 使用独立的 HMAC 密钥。",
];
const sandboxSignature: Text = [
  "Keep the private key on your server. Sandbox operators register its public key and key ID. Send application/json with the original bytes. The request timestamp is UTC Unix seconds and must be within 300 seconds. The nonce and signature are unpadded Base64URL; use a fresh random nonce and timestamp on retries, with the same payload and original idempotency key.",
  "私钥留在商户服务端，由运营登记公钥和密钥 ID。按 application/json 发送原始字节。时间戳用 UTC Unix 秒，允许偏差 300 秒。nonce 和签名使用无填充的 Base64URL。重试保留原请求和幂等键，重新生成随机 nonce 和时间戳。",
];
const webhookNote: Text = [
  "Configure a local loopback receiver. Verify the exact original bytes before parsing JSON. Persist and deduplicate the event ID before replying 2xx. Reject invalid or stale signatures and confirm conflicting updates by querying the original order.",
  "当前仅支持本地回环接收端。使用原始请求体字节验签，验签后再解析 JSON。持久化并去重事件编号后回复 2xx。签名不符或超时应拒绝，状态冲突时查询原订单。",
];
const webhook = merchantContract.webhooks.terminalBusinessEvent.post;
const webhookFields = [merchantContract.components.schemas.CollectionEvent, merchantContract.components.schemas.PayoutEvent];


function describe(schema: Schema): string {
  const reference = schema.$ref?.split("/").at(-1);
  if (reference) return describe(schemas[reference]);
  if (schema.const !== undefined) return schema.const;
  if (schema.items) return "array · " + describe(schema.items);
  if (schema.anyOf) return schema.anyOf.map(describe).join(" | ");
  return [schema.type || (schema.enum ? "enum" : "object"),
    schema.enum?.join(" / "), schema.pattern, schema.minimum !== undefined && schema.maximum !== undefined ?
      `${schema.minimum}–${schema.maximum}` : undefined].filter(Boolean).join(" · ");
}

function bodyRows(schema?: Schema, location = "body"): Parameter[] {
  if (!schema) return [];
  if (schema.$ref) return bodyRows(schemas[schema.$ref.split("/").at(-1)!], location);
  return [
    ...(schema.allOf?.flatMap(item => bodyRows(item, location)) ?? []),
    ...Object.entries(schema.properties ?? {}).flatMap(([name, value]) => value ? [{
      name, in: location, required: schema.required?.includes(name), schema: value,
    }] : []),
  ];
}

function detail(endpoint: typeof endpoints[number]) {
  const route: Route = merchantContract.paths[endpoint.path];
  const call = route[endpoint.method]!;
  const parameters: Parameter[] = [
    ...(route.parameters ?? []), ...(call.parameters ?? []),
    ...(endpoint.body ? [{ name: "Content-Type", in: "header", required: true, schema: { type: "string" } }] : []),
    ...bodyRows(call.requestBody?.content?.["application/json"].schema),
  ];
  const success = call.responses["201"] ?? call.responses["200"];
  return { parameters, responses: call.responses, responseFields: bodyRows(success.content?.["application/json"].schema, "response") };
}

function failureSamples(status: string, endpoint: typeof endpoints[number]) {
  const make = (code: string, message: string) => ({ error: { code, message } });
  const base = errors[status];
  const sample = make(base.code, base.message);
  const signedWrite = endpoint.method === "post";
  if (status === "401" && signedWrite) return [sample, make("invalid_request_signature", "Supply a valid signed request.")];
  if (status === "400") {
    if (endpoint.id === "list-payments") return [make("invalid_merchant_reference", "Supply a valid merchant reference.")];
    const body = make("invalid_body", "Send a valid JSON object matching the API contract.");
    if (endpoint.id === "create-payment") return [sample, body,
      make("invalid_payment", "Supply a valid merchant reference and a positive amount in minor units."),
      make("invalid_site_id", "Supply a valid site ID."),
      make("payment_method_unavailable", "The market, currency and method combination is not enabled for this site.")];
    return [sample, body, make("invalid_refund_amount", "Supply a positive refund amount in minor units.")];
  }
  if (status === "404" && endpoint.id === "get-refund") return [make("refund_not_found", "Refund not found.")];
  if (status === "404" && endpoint.id === "create-payment") return [make("site_not_found", "Site not found.")];
  const nonceError = make("request_nonce_reused", "Sign again with a fresh nonce and the original Idempotency-Key.");
  if (status === "409" && endpoint.id === "create-refund") return [nonceError,
    make("idempotency_conflict", "The key is already assigned to a different refund."),
    make("refund_unavailable", "This payment has no refundable original channel transaction."),
    make("refund_exceeds_balance", "That amount exceeds the remaining refundable balance."),
  ];
  if (status === "409") return [nonceError, sample, make("merchant_reference_exists", "A payment with that merchant reference already exists.")];
  if (status === "503") {
    const message = { capabilities: "Unable to read payment capabilities.", "create-payment": "Retry with the original Idempotency-Key.",
      "list-payments": "Unable to list payments.", "get-payment": "Unable to read the payment." } as Record<string, string>;
    return [sample, make("payment_store_unavailable", message[endpoint.id] ?? "The event or refund store is unavailable."),
      ...(signedWrite ? [make("signature_store_unavailable", "Retry with a fresh signature and the original Idempotency-Key.")] : [])];
  }
  return [sample];
}

function statusNote(status: string, zh: boolean) {
  if (status === "201") return zh ? "首次受理。处理结果见响应体的 status。" : "First acceptance. Inspect status in the response.";
  return zh ? "请求成功。写操作的幂等重试返回原记录。" : "Successful request. An idempotent retry returns the original record.";
}

function download(content: string, name: string, mime: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function fieldDescription(field: Parameter, zh: boolean) {
  return fieldNotes[field.name] ? choose(fieldNotes[field.name], zh) : field.description ?? field.schema.description ?? "—";
}

function Parameters({ values, zh, label = "Request parameters" }: { values: Parameter[]; zh: boolean; label?: string }) {
  const { t } = useLocale();
  if (!values.length) return <p className="muted">{t("No additional request parameters.")}</p>;
  return <div className="table-scroll" role="region" aria-label={t(label)} tabIndex={0}>
    <table><thead><tr><th scope="col">{t("Parameter")}</th><th scope="col">{t("Location")}</th>
      <th scope="col">{t("Required")}</th><th scope="col">{t("Type and description")}</th></tr></thead>
      <tbody>{values.map(field => <tr key={field.in + "/" + field.name}><th scope="row"><code>{field.name}</code></th>
        <td>{t(field.in)}</td><td>{t(field.name.startsWith("FlowXPay-Request-") || field.name === "FlowXPay-Key-ID" ? "Signed accounts" : field.required ? "Yes" : "No")}</td>
        <td><code>{describe(field.schema)}</code><div className="muted">{fieldDescription(field, zh)}</div></td>
      </tr>)}</tbody></table>
  </div>;
}

export function MerchantDocs() {
  const { t, locale } = useLocale();
  const zh = locale === "zh-CN";
  const [selected, select] = useState(endpoints[0].id);
  const [language, setLanguage] = useState<SampleLanguage>("cURL");
  const endpoint = endpoints.find(item => item.id === selected);
  const docs = endpoint && detail(endpoint);
  return <div className="merchant-docs">
    <section className="workspace-panel docs-intro">
      <div className="heading"><h2>{t("Merchant integration")}</h2>
        <div className="header-actions"><button type="button" className="secondary" onClick={() => download(json(merchantContract) + "\n", "flowxpay-merchant-openapi.json", "application/json")}>
          <DownloadSimple size={17} aria-hidden="true" />{t("Download OpenAPI")}
        </button></div>
      </div>
      <p>{choose(coverage, zh)}</p><p className="muted">{choose(intro, zh)}</p><p className="muted">{choose(signing, zh)}</p>
      <details className="docs-signing"><summary>{t("Sandbox request signing")}</summary><p className="muted">{choose(sandboxSignature, zh)}</p><Code source={signedRequestNode} /></details>
    </section>
    <section className="workspace-panel docs-reference">
      <div className="docs-select"><label htmlFor="docs-endpoint">{t("Endpoint")}</label>
        <select id="docs-endpoint" value={selected} onChange={event => select(event.target.value)}>
          {endpoints.map(item => <option key={item.id} value={item.id}>{choose(item.title, zh)}</option>)}
          <option value="webhook">{t("Webhook verification")}</option>
        </select>
      </div>
      {endpoint && docs ? <article key={endpoint.id}>
        <h2>{choose(endpoint.title, zh)}</h2>
        <div className="docs-path"><strong className="tag blue">{endpoint.method.toUpperCase()}</strong><code>/v1{endpoint.path}</code></div>
        <p>{choose(endpoint.note, zh)}</p>
        <h3>{t("Request parameters")}</h3><Parameters values={docs.parameters} zh={zh} />
        <div className="heading docs-example-heading">
          <h3>{t("Request example")}</h3>
          <select aria-label={t("Example language")} value={language} onChange={event => setLanguage(event.target.value as SampleLanguage)}>
            {languages.map(item => <option key={item}>{item}</option>)}
          </select>
        </div>
        <Code key={language} source={requestExample(endpoint, language)} />
        <h3>{t("Successful response")}</h3>
        {Object.keys(docs.responses).filter(status => status.startsWith("2")).map(status =>
          <p key={status}><code>{status}</code> {statusNote(status, zh)}</p>)}
        <Parameters values={docs.responseFields} zh={zh} label="Response fields" />
        <Code source={json(endpoint.output)} />
        <h3>{t("Error responses")}</h3>
        {Object.keys(docs.responses).filter(status => errors[status]).map(status => {
          return <details key={status}><summary><code>{status}</code> {choose(errors[status].note, zh)}</summary>
            {failureSamples(status, endpoint).map(sample => <Code key={sample.error.code} source={json(sample)} />)}</details>;
        })}
      </article> : <article>
        <h2>{t("Webhook verification")}</h2><p>{choose(webhookNote, zh)}</p>
        <h3>{t("Request parameters")}</h3><Parameters values={webhook.parameters} zh={zh} />
        {webhookFields.map((schema, index) => <details key={index}><summary>{t(index === 0 ? "Payment and refund event fields" : "Payout event fields")}</summary>
          <Parameters values={bodyRows(schema)} zh={zh} /></details>)}
        <h3>{t("Event example")}</h3><Code source={json(eventSample)} />
        <h3>{t("Verification example")} · Python</h3><Code source={webhookPython} />
      </article>}
    </section>
  </div>;
}
