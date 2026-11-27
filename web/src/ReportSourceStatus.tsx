import { useLocale } from "./locale";
import { base } from "./WorkspaceAccess";
import { LoadState, useWorkspaceData } from "./WorkspaceInventoryUI";

type Source = {
  account_id: string;
  currency: string;
  name: string;
  status: "unconfigured" | "awaiting_data" | "reported";
  last_synced_at: string | null;
};

export function ReportSourceStatus({ kind, token }: { kind: "dispute" | "statement"; token: string }) {
  const { t, dateTime } = useLocale();
  const sources = useWorkspaceData<Source[]>(base + "/" + kind + "-sources", token);
  const title = t(kind === "dispute" ? "Dispute data sources" : "Statement data sources");
  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{title}</h2>
      <button className="secondary" onClick={sources.reload}>{t("Refresh")}</button></div>
    <LoadState state={sources} label={title}>{rows => rows.length ? <ul>
      {rows.map(source => <li key={source.account_id + "/" + source.currency}>
        <strong>{source.name} · {source.currency}</strong>{" · "}
        {source.last_synced_at ? <time dateTime={source.last_synced_at}>
          {t("Last data received")} {dateTime(source.last_synced_at)}
        </time> : t(source.status === "unconfigured" ? "Data source not configured" : "Awaiting channel data")}
      </li>)}
    </ul> : <p className="muted empty">{t("No channel data sources are configured.")}</p>}</LoadState>
  </section>;
}
