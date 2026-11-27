import {SandboxRequests} from "./SandboxRequests";
import { memberPortal } from "./memberEnvironment";
import { ProductionFunds } from "./ProductionFunds";
import { ReportSourceStatus } from "./ReportSourceStatus";
import { useLocale } from "./locale";
import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError, request } from "./api";
import { Dialog } from "./Dialog";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";
import { base, message } from "./WorkspaceAccess";

const currencies = {
  mock_card: "USD", mock_pix: "BRL", mock_in_redirect: "INR", mock_in_qr: "INR", mock_in_unknown: "INR",
} as const;
type Account = { id: string; currency: string; name?: string; selection: string };
type AccountResponse = { id: string; provider: keyof typeof currencies; currency?: string; name?: string; products?: string[]; market?: string };
type Page<T> = { data: T[]; next_after?: string };
type Minor = number | string | null;
type Amounts = { gross_minor: Minor; fee_minor: Minor; net_minor: Minor; calculated_net_minor: Minor };
type Totals = Amounts & { batch_count: number; row_count: number };
type Batch = Amounts & {
  id: string; statement_date: string; settlement_status: string; withdrawal_status: string | null;
  withdrawal_reference?: string; row_count: number; imported_by: string; imported_at: string;
};
type Entry = Amounts & {
  id: string; kind: string; channel_reference: string; reverses_entry?: string; order_id?: string;
  order_status?: string; expected_gross_minor?: number; match_status: string; difference_status: string; revision: number;
};
type Order = Amounts & { id: string; status: string; expected_gross_minor: number; entry_count: number; channel_reference?: string };
type Audit = {
  revision: number; member_id: string; action: string; note: string; created_at: string;
  before_check: Entry; after_check: Entry;
};

const label = (status: string | null) => status?.replaceAll("_", " ") ?? "—";
const query = (cursor: string) => cursor ? "?after=" + encodeURIComponent(cursor) : "";
const dateText = (date: Date) => date.toISOString().slice(0, 10);

function useReportMoney() {
  const { exactMoney } = useLocale();
  return (value: Minor, currency: string) => value === null ? "—" : exactMoney(String(value), currency);
}

export async function readSettlement<T>(path: string, token?: string, init?: RequestInit): Promise<T> {
  // A single view can mount totals, batches and batch detail at the same time.
  for (const delay of [1000, 1000, 1000]) {
    try {
      return await request<T>(path, token, init);
    } catch (reason) {
      if (!(reason instanceof ApiError) || reason.status !== 429 || init?.signal?.aborted) throw reason;
      await new Promise(resolve => window.setTimeout(resolve, delay));
    }
  }
  return request<T>(path, token, init);
}

export function Settlement({ token, canManage, initialAccount, reconciliation, confirmations, canRequest=false }: {
  token: string; canManage: boolean; initialAccount: string; reconciliation: boolean; confirmations: boolean; canRequest?:boolean;
}) {
  const { t } = useLocale();
  return <>
    {!memberPortal && !confirmations && <SandboxRequests token={token} memberWrite={canManage}/>}
    {confirmations && <><ProductionFunds token={token} memberWrite={canRequest}/><SettlementConfirmations token={token} /></>}
    {!confirmations && reconciliation && memberPortal && <ReportSourceStatus token={token} kind="statement" />}
    {!confirmations && (reconciliation ? <StatementReconciliation token={token} canManage={canManage} initialAccount={initialAccount} /> :
      <section className="workspace-panel settlement-panel">
        <h2>{t("Statement reconciliation")}</h2>
        <p className="muted panel-note">{t("Channel statements and order-level reconciliation are unavailable in this workspace.")}</p>
      </section>)}
  </>;
}

type Confirmation = { id: string; paid_on: string; amount: string; currency: string; transfer_reference: string; confirmed_at: string };

export function SettlementConfirmations({ token }: { token: string }) {
  const { t, date, dateTime } = useLocale();
  const rows = useWorkspaceData<Confirmation[]>("/flowxpay/ops/api/settlements", token);
  const [selected, setSelected] = useState<Confirmation>();
  return <section className="workspace-panel settlement-panel">
    <div className="heading panel-heading"><h2>{t("Confirmed settlements")}</h2>
      <button className="secondary" onClick={rows.reload}>{t("Refresh")}</button></div>
    <p className="muted panel-note">{t("These transfers were recorded by platform operations. Individual order matches require channel statements.")}</p>
    <LoadState state={rows} label={t("Confirmed settlements")}>{data => data.length ?
      <WorkspaceTable label={t("Confirmed settlements")} columns={[t("Paid on"), t("Amount"), t("Reference")]}>
        {data.map(item => <tr key={item.id}><td>{date(item.paid_on)}</td><td>{item.amount} {item.currency}</td>
          <td><button className="text-button wrap" onClick={() => setSelected(item)}>{item.transfer_reference}</button></td></tr>)}
      </WorkspaceTable> : <p className="empty muted">{t("No confirmed settlements.")}</p>}</LoadState>
    {selected && <Dialog drawer title={t("Settlement details")} close={() => setSelected(undefined)}>
      <Details fields={[[t("Settlement ID"), selected.id], [t("Reference"), selected.transfer_reference],
        [t("Amount"), selected.amount + " " + selected.currency], [t("Paid on"), date(selected.paid_on)],
        [t("Confirmed at"), dateTime(selected.confirmed_at)]]} />
    </Dialog>}
  </section>;
}

function StatementReconciliation({ token, canManage, initialAccount }: { token: string; canManage: boolean; initialAccount: string }) {
  const { t } = useLocale();
  const accounts = useWorkspaceData<AccountResponse[]>(base + (memberPortal ? "/statement-accounts" : "/channel-accounts"), token);
  const [selected, setSelected] = useState(initialAccount);
  return <LoadState state={accounts} label={t("settlement accounts")}>{rows => {
    const values: Account[] = rows.map(row => ({
      id: row.id, name: memberPortal ? row.products?.join(" / ") || row.market : row.name, currency: row.currency ?? currencies[row.provider],
      selection: row.currency ? row.id + "/" + row.currency : row.id,
    }));
    const account = values.find(value => value.selection === selected) ?? values[0];
    return account ? <>
      <section className="workspace-panel settlement-panel settlement-controls">
        <label htmlFor="settlement-account">{t("Channel account")}</label>
        <select id="settlement-account" value={account.selection} onChange={event => setSelected(event.target.value)}>
          {values.map(value => <option key={value.selection} value={value.selection}>{value.currency}{value.name && <> · {value.name}</>}{!memberPortal && !value.name && <> · {value.id}</>}</option>)}
        </select>
        <p className="muted small">{t(memberPortal ? "Channel statements and reported withdrawal status." : "Sandbox statements and withdrawal status. Withdrawal initiation is disabled.")}</p>
      </section>
      <AccountSettlement key={account.selection} account={account} token={token} canManage={canManage} />
    </> : <p className="muted empty">{t("No channel accounts are available.")}</p>;
  }}</LoadState>;
}

function AccountSettlement({ account, token, canManage }: { account: Account; token: string; canManage: boolean }) {
  const { t, date } = useLocale();
  const money = useReportMoney();
  const currency = account.currency;
  const path = base + "/settlement/" + encodeURIComponent(account.id) + "/" + currency;
  const [view, setView] = useState<"batches" | "orders">("batches");
  const [selected, setSelected] = useState("");
  const [importing, setImporting] = useState(false);
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  return <>
    <div className="settlement-toolbar">
      <button type="button" className={view === "batches" ? "primary" : "secondary"} aria-pressed={view === "batches"}
        onClick={() => setView("batches")}>{t("Statement batches")}</button>
      <button type="button" className={view === "orders" ? "primary" : "secondary"} aria-pressed={view === "orders"}
        onClick={() => setView("orders")}>{t("Order report")}</button>
      {canManage && <button type="button" className="secondary" onClick={() => setImporting(true)}>{t("Import statement")}</button>}
    </div>
    {notice && <p role="status">{t(notice)}</p>}
    {view === "batches" ? <>
      <PeriodTotals refresh={revision} token={token} path={path} currency={currency} />
      <ReportPages<Batch> key={"batches-" + revision} token={token} endpoint={path + "/batches"} title={t("statement batches")}>
        {batches => <WorkspaceTable label={t("Statement batches")} columns={[t("Batch / date"), t("Settlement"), t("Withdrawal"), t("Gross"), t("Fees"), t("Reported net")]}>
          {batches.map(b => <tr key={b.id}>
            <td><button className="text-button" onClick={() => setSelected(b.id)}>{b.id}</button><br />{date(b.statement_date)}</td>
            <td>{t(label(b.settlement_status))}</td><td>{t(label(b.withdrawal_status))}</td>
            <td>{money(b.gross_minor, currency)}</td><td>{money(b.fee_minor, currency)}</td><td>{money(b.net_minor, currency)}</td>
          </tr>)}
        </WorkspaceTable>}
      </ReportPages>
      {selected && <BatchDetail key={selected} path={path} batch={selected} currency={currency} token={token} canManage={canManage}
        close={() => setSelected("")} />}
    </> : <OrderReport path={path} currency={currency} token={token} />}
    {importing && <ImportStatement endpoint={path + "/batches"} token={token} close={() => setImporting(false)}
      imported={(batch, replayed) => {
        setImporting(false);
        setView("batches");
        setNotice(replayed ? "Statement already imported." : "Statement imported.");
        setSelected(batch);
        setRevision(value => value + 1);
      }} />}
  </>;
}

function PeriodTotals({ token, path, currency, refresh }: { token: string; path: string; currency: string; refresh: number }) {
  const { t } = useLocale();
  const today = new Date();
  const [from, setFrom] = useState(dateText(new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1))));
  const [through, setThrough] = useState(dateText(today));
  const period = "?from=" + encodeURIComponent(from) + "&through=" + encodeURIComponent(through);
  return <section className="workspace-panel settlement-panel settlement-controls">
    <h2>{t("Account totals")}</h2>
    <p className="muted small">{t("Date range uses UTC.")}</p>
    <div className="settlement-toolbar">
      <label>{t("From")} <input id="settlement-from" type="date" value={from} onChange={event => setFrom(event.target.value)} /></label>
      <label>{t("Through")} <input id="settlement-through" type="date" value={through} onChange={event => setThrough(event.target.value)} /></label>
    </div>
    {from && through && <TotalsResult key={period + ":" + refresh} token={token} endpoint={path + "/totals" + period} currency={currency} />}
  </section>;
}

function TotalsResult({ token, endpoint, currency }: { token: string; endpoint: string; currency: string }) {
  const { t } = useLocale();
  const exactMoney = useReportMoney();
  const totals = useWorkspaceData<Totals>(endpoint, token, readSettlement);
  return <LoadState state={totals} label={t("account totals")}>{value => <Details fields={[
    [t("Batches / entries"), value.batch_count + " / " + value.row_count],
    [t("Gross"), exactMoney(value.gross_minor, currency)], [t("Fees"), exactMoney(value.fee_minor, currency)],
    [t("Reported net"), exactMoney(value.net_minor, currency)], [t("Calculated net"), exactMoney(value.calculated_net_minor, currency)],
  ]} />}</LoadState>;
}

function ReportPages<T>({ token, endpoint, title, children, exportPage }: {
  token: string; endpoint: string; title: string; children: (rows: T[]) => ReactNode;
  exportPage?: (cursor: string) => ReactNode;
}) {
  const [pages, setPages] = useState([""]);
  const cursor = pages[pages.length - 1];
  return <section className="workspace-panel settlement-panel">
    <ReportPage<T> key={cursor} token={token} endpoint={endpoint + query(cursor)} title={title}
      previous={pages.length > 1 ? () => setPages(values => values.slice(0, -1)) : undefined}
      next={next => setPages(values => [...values, next])} download={exportPage?.(cursor)}>{children}</ReportPage>
  </section>;
}

function ReportPage<T>({ token, endpoint, title, children, previous, next, download }: {
  token: string; endpoint: string; title: string; children: (rows: T[]) => ReactNode;
  previous?: () => void; next: (cursor: string) => void; download?: ReactNode;
}) {
  const { t } = useLocale();
  const page = useWorkspaceData<Page<T>>(endpoint, token, readSettlement);
  return <LoadState state={page} label={title}>{value => <>
    {value.data.length ? children(value.data) : <p className="muted empty">{t("No {resource} in this account.", { resource: title })}</p>}
    {(previous || value.next_after || (download && value.data.length > 0)) && <div className="settlement-toolbar">
      {previous && <button className="secondary" onClick={previous}>{t("Previous page")}</button>}
      {value.next_after && <button className="secondary" onClick={() => next(value.next_after!)}>{t("Next page")}</button>}
      {value.data.length > 0 && download}
    </div>}
  </>}</LoadState>;
}

function OrderReport({ token, path, currency }: { token: string; path: string; currency: string }) {
  const { t } = useLocale();
  const money = useReportMoney();
  const [kind, setKind] = useState("collection");
  const endpoint = path + "/orders/" + kind;
  return <>
    <div className="workspace-panel settlement-panel">
      <label htmlFor="settlement-order-kind">{t("Order type")}</label>
      <select id="settlement-order-kind" value={kind} onChange={event => setKind(event.target.value)}>
        <option value="collection">{t("Collections")}</option>{!memberPortal && <option value="refund">{t("Refunds")}</option>}<option value="payout">{t("Payouts")}</option>
      </select>
    </div>
    <ReportPages<Order> key={kind} token={token} endpoint={endpoint} title={t(kind === "collection" ? "collection orders" : kind === "refund" ? "refund orders" : "payout orders")}
      exportPage={cursor => <Download endpoint={endpoint + "/report.csv" + query(cursor)} token={token} name={kind + "-orders"} label={t("Export page CSV")} />}>
      {orders => <WorkspaceTable label={t("Order reconciliation")} columns={["Order", t("Platform status"), t("Expected gross"), t("Statement rows"), t("Reported gross"), t("Fees"), t("Reported net")]}>
        {orders.map(order => <tr key={order.id}><td>{order.id}<br /><span className="muted small">{order.channel_reference}</span></td>
          <td>{t(label(order.status))}</td><td>{money(order.expected_gross_minor, currency)}</td>
          <td>{order.entry_count || t("Missing")}</td><td>{order.entry_count ? money(order.gross_minor, currency) : "—"}</td>
          <td>{order.entry_count ? money(order.fee_minor, currency) : "—"}</td>
          <td>{order.entry_count ? money(order.net_minor, currency) : "—"}</td></tr>)}
      </WorkspaceTable>}
    </ReportPages>
  </>;
}

function Download({ endpoint, token, name, label: buttonLabel }: { endpoint: string; token: string; name: string; label: string }) {
  const { t } = useLocale();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function download() {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(endpoint, { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
      if (!response.ok) {
        const body = await response.json().catch(() => undefined);
        throw new Error(body?.error?.message ?? "Download failed.");
      }
      const href = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = href;
      link.download = name + ".csv";
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(href), 1000);
    } catch (reason) { setError(message(reason)); }
    finally { setBusy(false); }
  }
  return <><button className="secondary" onClick={() => void download()} disabled={busy}>{buttonLabel}</button>
    {error && <p className="error" role="alert">{t(error)}</p>}</>;
}

function BatchDetail({ path, batch, currency, token, canManage, close }: {
  path: string; batch: string; currency: string; token: string; canManage: boolean; close: () => void;
}) {
  const { t, date, dateTime } = useLocale();
  const money = useReportMoney();
  const endpoint = path + "/batches/" + encodeURIComponent(batch);
  const result = useWorkspaceData<Batch & { entries: Entry[] }>(endpoint, token, readSettlement);
  const [entry, setEntry] = useState<Entry>();
  if (entry) return <EntryDetail entry={entry} path={path} token={token} currency={currency} canManage={canManage}
    close={() => { setEntry(undefined); result.reload(); }} updated={setEntry} />;
  return <Dialog drawer title={t("Statement batch")} close={close}>
    <LoadState state={result} label={t("statement batch")}>{data => <>
      <Details fields={[
        [t("Batch"), data.id], [t("Statement date"), date(data.statement_date)], [t("Settlement"), t(label(data.settlement_status))],
        [t("Withdrawal"), t(label(data.withdrawal_status))], [t("Withdrawal reference"), data.withdrawal_reference ?? "—"],
        [t("Gross"), money(data.gross_minor, currency)], [t("Fees"), money(data.fee_minor, currency)],
        [t("Reported net"), money(data.net_minor, currency)], [t("Calculated net"), money(data.calculated_net_minor, currency)],
        [t("Imported by"), data.imported_by], [t("Imported at"), dateTime(data.imported_at)],
      ]} />
      <div className="settlement-toolbar"><Download endpoint={endpoint + "/entries.csv"} token={token} name={batch} label={t("Export batch CSV")} /></div>
      <WorkspaceTable label={t("Statement entries")} columns={[t("Entry"), t("Net"), t("Reconciliation")]}>
        {data.entries.map(row => <tr key={row.id}><td><button className="text-button" onClick={() => setEntry(row)}>{row.id}</button>
          <br /><span className="muted small">{t(label(row.kind))}</span></td><td>{money(row.net_minor, currency)}</td>
          <td>{t(label(row.difference_status))}</td></tr>)}
      </WorkspaceTable>
    </>}</LoadState>
  </Dialog>;
}

function EntryDetail({ entry, path, token, currency, canManage, close, updated }: {
  entry: Entry; path: string; token: string; currency: string; canManage: boolean;
  close: () => void; updated: (next: Entry) => void;
}) {
  const { t, dateTime } = useLocale();
  const money = useReportMoney();
  const endpoint = path + "/entries/" + encodeURIComponent(entry.id) + "/actions";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  async function act(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget;
    const data = new FormData(form);
    setBusy(true);
    setError("");
    try {
      updated(await request<Entry>(endpoint, token, { method: "POST", body: JSON.stringify({
        action: data.get("action"), note: data.get("note"), expected_revision: entry.revision,
      }) }));
      form.reset();
    } catch (reason) {
      setError(message(reason));
      setConflict(reason instanceof ApiError && reason.status === 409);
    } finally { setBusy(false); }
  }
  return <Dialog drawer title={t("Statement entry")} close={close} locked={busy}>
    <Details fields={[
      [t("Entry"), entry.id], [t("Channel reference"), entry.channel_reference], [t("Reverses"), entry.reverses_entry ?? "—"],
      [t("Platform order"), entry.order_id ?? "—"], [t("Platform status"), entry.order_status ? t(label(entry.order_status)) : "—"],
      [t("Expected gross"), entry.expected_gross_minor === undefined ? "—" : money(entry.expected_gross_minor, currency)],
      [t("Gross"), money(entry.gross_minor, currency)], [t("Fees"), money(entry.fee_minor, currency)],
      [t("Reported net"), money(entry.net_minor, currency)], [t("Calculated net"), money(entry.calculated_net_minor, currency)],
      [t("Order match"), t(label(entry.match_status))], [t("Difference"), t(label(entry.difference_status))],
    ]} />
    {canManage && <form className="payment-form" onSubmit={act}>
      <fieldset className="workspace-fields" disabled={busy || conflict}>
        <label htmlFor="settlement-action">{t("Action")}</label><select id="settlement-action" name="action">
          {entry.difference_status === "open" && <option value="acknowledge">{t("Acknowledge difference")}</option>}
          {entry.difference_status === "acknowledged" && <option value="reopen">{t("Reopen difference")}</option>}
          <option value="recheck">{t("Recheck platform order")}</option>
        </select>
        <label htmlFor="settlement-note">{t("Audit note")}</label>
        <textarea id="settlement-note" name="note" maxLength={300} required rows={2} />
      </fieldset>
      {error && <p className="error" role="alert">{t(error)}</p>}
      {conflict ? <button type="button" className="secondary" onClick={close}>{t("Back to batch")}</button> :
        <button className="primary" disabled={busy}>{t("Record action")}</button>}
    </form>}
    {!memberPortal && <><h3>{t("Action history")}</h3>
    <ReportPages<Audit> key={entry.revision} token={token} endpoint={endpoint} title={t("actions")}>{actions =>
      <WorkspaceTable label={t("Action history")} columns={[t("Action / by"), t("Note"), t("Difference")]}>
        {actions.map(a => <tr key={a.revision}><td>{t(label(a.action))}<br />{a.member_id}<br />{dateTime(a.created_at)}</td>
          <td className="wrap">{a.note}</td><td>{t(label(a.before_check.difference_status))} → {t(label(a.after_check.difference_status))}</td></tr>)}
      </WorkspaceTable>}
    </ReportPages></>}
  </Dialog>;
}

function ImportStatement({ token, endpoint, close, imported }: {
  token: string; endpoint: string; close: () => void; imported: (id: string, replayed: boolean) => void;
}) {
  const { t } = useLocale();
  const pending = useRef(false);
  const previous = useRef<string>(undefined);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [uncertain, setUncertain] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    const file = new FormData(event.currentTarget).get("statement");
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      if (previous.current === undefined) {
        if (!(file instanceof File) || !file.size || file.size > 256 * 1024) {
          setError("Select a JSON statement up to 256 KiB.");
          return;
        }
        previous.current = await file.text();
      }
      const result = await request<{ id: string; replayed: boolean }>(endpoint, token, { method: "POST", body: previous.current });
      imported(result.id, result.replayed);
    } catch (reason) {
      const unknown = uncertain || !(reason instanceof ApiError) || reason.status >= 500;
      setUncertain(unknown);
      if (!unknown) previous.current = undefined;
      setError(message(reason));
    } finally { pending.current = false; setBusy(false); }
  }
  return <Dialog drawer title={t("Import sandbox statement")} close={close} locked={busy}>
    <form className="payment-form" onSubmit={submit}>
      <fieldset className="workspace-fields" disabled={busy || uncertain}>
        <label htmlFor="settlement-file">{t("Statement JSON")}</label>
        <input id="settlement-file" name="statement" type="file" accept=".json,application/json" required />
      </fieldset>
      {error && <p className="error" role="alert">{t(error)}</p>}
      <button className="primary" disabled={busy}>{busy ? t("Importing…") : uncertain ? t("Retry same statement") : t("Import")}</button>
    </form>
  </Dialog>;
}
