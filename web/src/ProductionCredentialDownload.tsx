import { useState } from "react";
import { useLocale } from "./locale";

export interface ProductionKey { id: string; token: string; private_key_pem: string }

export function productionCredentialContents(merchant: string, apiKey: ProductionKey) {
  return JSON.stringify({ environment: "production", merchant_id: merchant, key_id: apiKey.id,
    token: apiKey.token, private_key_pem: apiKey.private_key_pem }, null, 2);
}

export function ProductionCredentialDownload({ merchant, apiKey }: { merchant: string; apiKey: ProductionKey }) {
  const { t, locale } = useLocale();
  const zh = locale === "zh-CN";
  const [copyState, setCopyState] = useState<"idle" | "copied" | "manual">("idle");
  const contents = productionCredentialContents(merchant, apiKey);

  async function copy() {
    try {
      await navigator.clipboard.writeText(contents);
      setCopyState("copied");
    } catch {
      setCopyState("manual");
    }
  }

  function download() {
    const url = URL.createObjectURL(new Blob([contents], { type: "application/json" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `flowxpay-${merchant}-credentials.json`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return <>
    <p><code>{apiKey.id}</code></p>
    <p>{zh ? "公钥已登记。关闭前请复制 API 凭据，通过私密渠道交给商户技术人员。凭据包含 Token 和签名私钥。" : "The public key is registered. Before closing, copy the API credentials and deliver them privately to the merchant developer. They include the token and signing private key."}</p>
    <button type="button" className="primary" onClick={copy}>{zh ? "复制 API 凭据" : "Copy API credentials"}</button>
    <button type="button" className="secondary" onClick={download}>{t("Download API credentials")}</button>
    <span role="status">{copyState === "copied" && (zh ? "凭据已复制" : "Credentials copied")}</span>
    {copyState === "manual" && <label>{zh ? "请长按全选并复制" : "Select all and copy"}
      <textarea readOnly rows={7} value={contents} onFocus={event => event.currentTarget.select()} />
    </label>}
  </>;
}
