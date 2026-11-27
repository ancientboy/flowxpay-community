import { useState } from "react";
import { Copy } from "@phosphor-icons/react";
import { useLocale } from "./locale";

export function MerchantDocCode({ source, language }: { source: string; language?: string }) {
  const { t } = useLocale();
  const [message, setMessage] = useState("");
  async function copy() {
    try {
      await navigator.clipboard.writeText(source);
      setMessage(t("Copied"));
    } catch {
      setMessage(t("Select the code to copy it manually."));
    }
  }
  return <div className="docs-code">
    <div className="docs-code-actions">
      <span>{language}</span>
      <span role="status">{message}</span>
      <button type="button" className="text-button" onClick={() => void copy()}>
        <Copy size={16} aria-hidden="true" />{t("Copy code")}
      </button>
    </div>
    <pre tabIndex={0} aria-label={language}><code>{source}</code></pre>
  </div>;
}
