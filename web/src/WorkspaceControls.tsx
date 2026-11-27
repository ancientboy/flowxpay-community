import { ProductionCredentialDownload, type ProductionKey } from "./ProductionCredentialDownload";
import { useLocale } from "./locale";
import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError } from "./api";
import { channelSubtitle, channelTitle } from "./ChannelIdentity";
import type { ChannelAccount } from "./MemberWorkspace";
import { Dialog } from "./Dialog";
import { base, message, post } from "./WorkspaceAccess";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

export interface APIKey {
  id: string;
  label: string;
  legacy: boolean;
  platform: boolean;
  created_at?: string;
  created_by?: string;
  replaces_id?: string;
  revoked_at?: string;
  revoked_by?: string;
}

export interface IssuedKey extends APIKey { secret: string; credentials?: { merchant: string; apiKey: ProductionKey } }

export interface RouteChange {
  id: string;
  site_id: string;
  account_id: string;
  market: string;
  currency: string;
  method: string;
  submitted_at: string;
  submitted_by: string;
  approved_at?: string;
  approved_by?: string;
  superseded_at?: string;
  cancelled_at?: string;
  platform: boolean;
}

function auditEntry(dateTime: (value: string) => string, actor?: string, at?: string) {
  return at ? <>{actor}<br />{dateTime(at)}</> : "—";
}

export function ControlAction({ title, close, refresh, submitLabel, action, children, unavailable, productionKey = false }: {
  title: string;
  close: () => void;
  refresh: () => void;
  submitLabel: string;
  unavailable?: boolean;
  productionKey?: boolean;
  action: (fields: FormData) => Promise<IssuedKey | void>;
  children: ReactNode;
}) {
  const { t } = useLocale();
  const pending = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [checkList, setCheckList] = useState(false);
  const [issued, setIssued] = useState<IssuedKey>();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || checkList || unavailable) return;
    const fields = new FormData(event.currentTarget);
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await action(fields);
      if (result) setIssued(result);
      else { refresh(); close(); }
    } catch (reason) {
      if (!(reason instanceof ApiError) || reason.status >= 500) {
        setCheckList(true);
        setError("The outcome could not be confirmed. Refresh the list before trying again.");
      } else {
        setError(message(reason));
        if ([404, 409].includes(reason.status)) refresh();
      }
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  return <Dialog drawer title={issued ? t("Save your API key") : title} locked={busy} close={() => {
    if (checkList || issued) refresh();
    close();
  }}>
    {issued ? <>
      {issued.credentials ? <ProductionCredentialDownload {...issued.credentials} /> : <>
      <p>{t(productionKey ? "Copy this production key now. This is the only time its secret is shown." : "Copy this sandbox key now. This is the only time its secret is shown.")}</p>
      <label htmlFor="issued-key">{t("API key secret")}</label>
      <input id="issued-key" value={issued.secret} readOnly autoComplete="off"
        spellCheck={false} onFocus={event => event.currentTarget.select()} />
      </>}
      <div className="detail-actions"><button className="primary" type="button" onClick={() => { refresh(); close(); }}>{t("Done")}</button></div>
    </> : <form className="payment-form" onSubmit={submit}>
      <fieldset className="workspace-fields" disabled={busy || checkList}>{children}</fieldset>
      {error && <p role="alert" className="error">{t(error)}</p>}
      {checkList ?
        <button type="button" className="secondary" onClick={() => { refresh(); close(); }}>{t("Close and refresh list")}</button> :
        <button className="primary" disabled={busy || unavailable}>{busy ? t("Saving…") : submitLabel}</button>}
    </form>}
  </Dialog>;
}

export function APIKeys({ token, environment }: { token: string; environment: "Production" | "Sandbox" }) {
  const { t, dateTime } = useLocale();
  const keys = useWorkspaceData<APIKey[]>(base + "/api-keys", token);
  const [selected, setSelected] = useState<APIKey>();
  const [action, setAction] = useState<"rotate" | "revoke">();
  const close = () => { setSelected(undefined); setAction(undefined); };

  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t(environment === "Production" ? "API keys" : "Sandbox credentials")}</h2>
      <div className="header-actions">
        <button className="secondary" onClick={keys.reload}>{t("Refresh keys")}</button>
      </div>
    </div>
    <LoadState state={keys} label={t("API keys")}>{items =>
      <WorkspaceTable label={t(environment === "Production" ? "API keys" : "Sandbox API keys")} columns={[t("Key"), t("Status"), t("Created by"), t("Retired by")]}>
        {items.map(item => <tr key={item.id}>
          <td><button className="text-button" onClick={() => setSelected(item)}>{item.label}</button>
            <span className="resource-id account-id muted">{item.id}</span></td>
          <td>{item.revoked_at ? t("Retired") : t("Active")}</td>
          <td>{auditEntry(dateTime, item.created_by, item.created_at)}</td>
          <td>{auditEntry(dateTime, item.revoked_by, item.revoked_at)}</td>
        </tr>)}
      </WorkspaceTable>
    }</LoadState>
    {selected && action && <ControlAction title={t(action === "rotate" ? "Rotate API key" : "Revoke API key")}
      close={close} refresh={keys.reload} submitLabel={t(action === "rotate" ? "Confirm rotation" : "Confirm revocation")}
      action={async () => {
        const path = "/api-keys/" + encodeURIComponent(selected.id) + "/" + action;
        if (action === "rotate") return post<IssuedKey>(path, {}, token);
        await post<APIKey>(path, {}, token);
      }}>
      <p>{t(action === "rotate" ? "Rotation issues a new secret and immediately retires the current key." :
        "The key will immediately stop authorizing new API requests.")}</p>
      <p className="wrap">{selected.label} · {selected.id}</p>
    </ControlAction>}
    {selected && !action && <Dialog drawer title={t("API key details")} close={close}>
      <Details fields={[
        [t("Label"), selected.label], [t("Key ID"), selected.id],
        [t("Type"), t(selected.legacy ? "Legacy sandbox key" : selected.platform ? "Platform-issued key" : "Merchant-managed key")],
        [t("Created"), auditEntry(dateTime, selected.created_by, selected.created_at)], [t("Replaces"), selected.replaces_id ?? "—"],
        [t("Retired"), auditEntry(dateTime, selected.revoked_by, selected.revoked_at)],
      ]} />
      {import.meta.env.MODE !== "community-demo" && !selected.platform && !selected.revoked_at && <div className="detail-actions">
        <button className="secondary" type="button" onClick={() => setAction("rotate")}>{t("Rotate key")}</button>
        <button className="secondary danger" type="button" onClick={() => setAction("revoke")}>{t("Revoke key")}</button>
      </div>}
    </Dialog>}
  </section>;
}

export function CollectionRouting({ token, accessHistory }: { token: string; accessHistory: boolean }) {
  return accessHistory ? <RouteAccessHistory token={token} /> : <RouteChangeHistory token={token} />;
}

function RouteHistory<T extends { id: string }>({ state, title, note, columns, label, subtitle, row, fields, refresh }: {
  state: ReturnType<typeof useWorkspaceData<T[]>>;
  title: string;
  note: string;
  columns: string[];
  label: (item: T) => string;
  subtitle?: (item: T) => string | undefined;
  row: (item: T) => ReactNode[];
  fields: (item: T) => [string, ReactNode][];
  refresh: () => void;
}) {
  const { t } = useLocale();
  const [selected, setSelected] = useState<T>();
  return <section className="workspace-panel collection-routing-panel">
    <div className="heading panel-heading"><h2>{t(title)}</h2>
      <div className="header-actions"><button className="secondary" onClick={refresh}>{t("Refresh routes")}</button></div>
    </div>
    <p className="muted panel-note">{t(note)}</p>
    <LoadState state={state} label={t("route changes")}>{items => items.length ?
      <WorkspaceTable label={t(title)} columns={[t("Route"), ...columns]}>
        {items.map(item => <tr key={item.id}>
          <td><button className="text-button" onClick={() => setSelected(item)}>{label(item)}</button>
            {subtitle?.(item) && <span className="channel-subtitle muted small">{subtitle(item)}</span>}</td>
          {row(item).map((value, index) => <td key={columns[index]} className="wrap">{value}</td>)}
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("There are no recorded route changes.")}</p>}</LoadState>
    {selected && <Dialog drawer title={t("Route change details")} close={() => setSelected(undefined)}>
      <Details fields={fields(selected)} />
    </Dialog>}
  </section>;
}

type RouteAccessEvent = {
  id: string;
  occurred_at: string;
  action: "routing.enabled" | "routing.disabled";
  product_id: string;
  market: string;
  currency: string;
  method: string;
};

function RouteAccessHistory({ token }: { token: string }) {
  const { t, dateTime } = useLocale();
  const changes = useWorkspaceData<RouteAccessEvent[]>(base + "/route-changes", token);
  const action = (item: RouteAccessEvent) => t(item.action === "routing.enabled" ? "Access enabled" : "Access disabled");
  return <RouteHistory state={changes} title="Collection routing history"
    note="Recent collection access changes recorded by platform operations."
    columns={[t("Method"), t("Action"), t("Date")]} label={item => item.product_id} refresh={changes.reload}
    row={item => [item.currency + " · " + t(item.method), action(item), dateTime(item.occurred_at)]}
    fields={item => [[t("Change ID"), item.id], [t("Product ID"), item.product_id],
      [t("Method"), item.market + " · " + item.currency + " · " + t(item.method)],
      [t("Action"), action(item)], [t("Date"), dateTime(item.occurred_at)]]} />;
}

function RouteChangeHistory({ token }: { token: string }) {
  const { t, dateTime } = useLocale();
  const changes = useWorkspaceData<RouteChange[]>(base + "/route-changes", token);
  const sites = useWorkspaceData<{ id: string; name: string }[]>(base + "/sites", token);
  const accounts = useWorkspaceData<ChannelAccount[]>(base + "/channel-accounts", token);
  const accountFor = (item: RouteChange) => accounts.data?.find(account => account.id === item.account_id);
  const routeTitle = (item: RouteChange) => {
    const account = accountFor(item);
    return account ? channelTitle(account, t) : item.currency + " · " + t(item.method);
  };
  const siteName = (id: string) => sites.data?.find(site => site.id === id)?.name ?? id;

  function displayStatus(item: RouteChange) {
    const status = routeStatus(item);
    if (accountFor(item)?.enabled !== false) return status;
    if (status === "Awaiting approval") return "Proposed channel unavailable";
    return status === "In effect" ? "Disabled" : status;
  }

  return <RouteHistory state={changes} title="Collection route changes"
    note="Platform operators propose and approve routes. This workspace shows your routing history."
    columns={[t("Site"), t("Status"), t("Submitted by"), t("Approved by")]} label={routeTitle}
    refresh={() => { changes.reload(); accounts.refresh(); }}
    subtitle={item => {
      const account = accountFor(item);
      return account && channelSubtitle(account.provider, t);
    }}
    row={item => [siteName(item.site_id), t(displayStatus(item)), auditEntry(dateTime, item.submitted_by, item.submitted_at),
      auditEntry(dateTime, item.approved_by, item.approved_at)]}
    fields={item => [[t("Change ID"), item.id], [t("Site"), siteName(item.site_id)], [t("Channel"), routeTitle(item)],
      [t("Account ID"), item.account_id], [t("Method"), item.market + " · " + item.currency + " · " + t(item.method)],
      [t("Status"), t(displayStatus(item))], [t("Submitted"), auditEntry(dateTime, item.submitted_by, item.submitted_at)],
      [t("Approved"), auditEntry(dateTime, item.approved_by, item.approved_at)],
      [t("Superseded"), item.superseded_at ? dateTime(item.superseded_at) : "—"]]} />;
}

export function routeStatus(change: RouteChange) {
  if (change.cancelled_at) return "Cancelled";
  if (change.superseded_at) return "Superseded";
  if (change.approved_at) return "In effect";
  return change.platform ? "Awaiting approval" : "Historical proposal";
}
