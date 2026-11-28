import {SettlementRequests} from "./SettlementRequests";
import {StatementImports} from "./StatementImports";
import { FundEvidence } from "./FundEvidence";
import { FundHistory } from "./FundHistory";
import { useEffect, useRef, useState } from "react";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";
import { productionControl } from "./ProductionPlatformControls";

import { ControlAction } from "./WorkspaceControls";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

type FundAccount = {
  id: string; currency: string; scale: number | null; available_minor: string; payout_held_minor: string;
  settlement_held_minor: string; settle_available_minor: string; dispute_held_minor:string; awaiting_upstream_minor:string; deficit_minor:string; refund_pending: boolean;
};
type Route = { channel_id: string; channel_name: string; direction: "collection" | "payout"; currency: string;
  account_id: string | null; wait_upstream?:boolean; maturity_days: number | null };
type BindingChange = { id: number; channel_id: string; direction: Route["direction"]; currency: string;
  previous_account_id?: string; previous_maturity_days?: number; account_id: string; maturity_days: number;
  changed_at: string; changed_by: string };
type Batch = {
  id: string; state: "locked" | "confirmed" | "cancelled"; account_id: string; currency: string;
  source_minor: string; source_scale: number; destination_currency: string; destination_amount: string;
  fx_rate: string; created_at: string; created_by?: string; closed_at?: string; closed_by?: string;
  transfer_reference?: string; paid_on?: string; cancel_reason?: string;
};
type Refund = { batch_id: string; channel_reference: string; applied_by?: string; channel_id: string; entry_id: string; currency: string; order_id?: string; account_id?: string;
  review_status: string; gross_minor: string; fee_minor: string; net_minor: string; fee_return_minor?: string; correction_id?: string; correction_candidate?: string; recovery_note?: string; note?: string };
type CurrencySettlement = { currency: string; scale: number; eligible_minor: string; locked_minor: string; confirmed_minor: string };
type Funds = { currency_settlements?: CurrencySettlement[]; accounts: FundAccount[]; currencies: string[]; routes: Route[]; binding_history: BindingChange[]; batches: Batch[]; refunds: Refund[] };
type Action =
  | { type: "bind"; route: Route }
  | { type: "lock"; account: FundAccount; request: string; currencies: string[] }
  | { type: "confirm" | "cancel"; batch: Batch }
  | { type: "refund"; refund: Refund };

const titles = {
  bind: "Bind funds account", lock: "Lock settlement batch", confirm: "Confirm offline transfer",
  cancel: "Cancel settlement batch", refund: "Apply statement refund",
};
const batchLabels = { locked: "Locked", confirmed: "Confirmed", cancelled: "Cancelled" };
const refundLabels: Record<string, string> = {
  posted: "Applied", recovery_pending: "Refund reversal pending", recovered: "Refund reversal posted", corrected: "Corrected by statement", historical: "Historical review completed", missing_order: "Missing order", historical_review: "Historical review",
  amount_mismatch: "Amount mismatch", pending: "Pending", ready_to_apply: "Ready to apply",
};

export function ProductionFunds({ token, merchant, operator = false, summaryOnly = false, memberWrite=false, focusBindings, focusDifferences, settlementInFinance, openOrder, openSource }: { token?: string; merchant?: string; operator?: boolean; summaryOnly?: boolean; memberWrite?:boolean; focusBindings?:boolean;focusDifferences?:boolean;settlementInFinance?:boolean;openOrder?:(id:string)=>void;openSource?:(direction:"collection"|"payout",id:string)=>void }) {
  const { t, exactMoney, date, dateTime, locale } = useLocale();
  const zh = locale === "zh-CN";
  const endpoint = "/flowxpay/ops/api/funds" + (merchant ? "?" + new URLSearchParams({ merchant_id: merchant }) : "");
  const state = useWorkspaceData<Funds>(endpoint, token ?? "");
  const [action, setAction] = useState<Action>();
  const [history,setHistory]=useState<{account:string;currency:string;scale:number|null;batch?:string}>();
  const bindingHeading = useRef<HTMLHeadingElement>(null);
  const loaded = Boolean(state.data);
  useEffect(() => {
    if (loaded && focusBindings) bindingHeading.current?.focus();
  }, [loaded, focusBindings]);
  const [detail, setDetail] = useState<Batch>();
  const showAmount = (amount: string, currency: string, scale: number | null) =>
    scale === null ? amount + " " + t("minor units") : exactMoney(amount, currency, scale);
  const direction = (value: Route["direction"]) => t(value === "payout" ? "Payout" : "Collection");

  return <>
    <LoadState state={state} label={t("Funds accounts")}>{data => <>
      {!merchant && !!data.currency_settlements?.length && <section className="workspace-panel">
        <div className="heading panel-heading"><h2>{zh ? "人工结算概览" : "Manual settlement overview"}</h2></div>
        <WorkspaceTable label={zh ? "人工结算概览" : "Manual settlement overview"} columns={[
          t("Currency"), t("Eligible for settlement"), zh ? "待人工付款" : "Awaiting offline payment", zh ? "累计已确认结算" : "Total settlement confirmed",
        ]}>{data.currency_settlements.map(row => <tr key={row.currency}>
          <td>{row.currency}</td>
          <td>{showAmount(row.eligible_minor,row.currency,row.scale)}</td>
          <td>{showAmount(row.locked_minor,row.currency,row.scale)}</td>
          <td>{showAmount(row.confirmed_minor,row.currency,row.scale)}</td>
        </tr>)}</WorkspaceTable>
      </section>}
      <section className={"workspace-panel " + (!merchant && !summaryOnly ? "merchant-funds-responsive" : "")}>
        <div className="heading panel-heading"><h2>{t("Funds accounts")}</h2><button className="secondary" onClick={state.reload}>{t("Refresh")}</button></div>
        {merchant && operator && <FundEvidence merchant={merchant} accounts={data.accounts} routes={data.routes} batches={data.batches} refresh={state.refresh} />}
        {summaryOnly && data.accounts.length > 0 ? <div className="merchant-fund-cards">
          {data.accounts.map(account => {
            const amount = (value:string) => showAmount(value,account.currency,account.scale);
            const openActivity = () => setHistory({account:account.id,currency:account.currency,scale:account.scale});
            return <article className="platform-card" key={account.currency+"/"+account.id}>
              <header className="heading">
                <h3>{account.currency} <span className="muted small">{account.id}</span></h3>
                <button type="button" className="text-button" onClick={openActivity}>{zh ? "资金流水" : "Account activity"}</button>
              </header>
              <dl>
                <div><dt>{zh ? "可用于代付" : "Available for payouts"}</dt><dd><button type="button" className="text-button" onClick={openActivity}>{amount(account.available_minor)}</button></dd></div>
                <div><dt>{t("Payout hold")}</dt><dd>{amount(account.payout_held_minor)}</dd></div>
                <div><dt>{t("Eligible for settlement")}</dt><dd><button type="button" className="text-button" onClick={openActivity}>{amount(account.settle_available_minor)}</button></dd></div>
                {account.settlement_held_minor !== "0" && <div><dt>{t("Settlement hold")}</dt><dd>{amount(account.settlement_held_minor)}</dd></div>}
                {BigInt(account.awaiting_upstream_minor ?? "0") > 0 && <div><dt>{zh ? "暂不可用于代付" : "Currently excluded from payouts"}</dt><dd>{amount(account.awaiting_upstream_minor)}</dd></div>}
              </dl>
              {account.deficit_minor!=="0" && <p>{t("Account deficit")} · {amount(account.deficit_minor)}</p>}
              {account.refund_pending && <p className="muted small">{t("Refund review pending")}</p>}
            </article>;
          })}
        </div> : data.accounts.length ? <WorkspaceTable label={t("Funds accounts")} columns={[
          t("Funds account"), t("Currency"), t("Available"), t("Payout hold"), t("Settlement hold"), t("Eligible for settlement"),
          ...(operator ? [t("Action")] : []),
        ]}>{data.accounts.map(account => {
          const amount = (value: string) => showAmount(value, account.currency, account.scale);
          return <tr key={account.currency + "/" + account.id}>
            <td data-label={t("Funds account")}>{account.id}</td><td data-label={t("Currency")}>{account.currency}</td><td data-label={zh ? "可用于代付" : "Available for payouts"}><button className="text-button" onClick={()=>setHistory({account:account.id,currency:account.currency,scale:account.scale})}>{amount(account.available_minor)}</button>{account.deficit_minor!=="0" && <div>{t("Account deficit")} · {amount(account.deficit_minor)}</div>}{account.awaiting_upstream_minor!=="0" && <div>{(zh ? "暂不可用于代付" : "Currently excluded from payouts")} · {amount(account.awaiting_upstream_minor)}</div>}</td>
            <td data-label={t("Payout hold")}>{amount(account.payout_held_minor)}</td><td data-label={t("Settlement hold")}>{amount(account.settlement_held_minor)}{account.dispute_held_minor!=="0" && <div>{t("Dispute hold")} · {amount(account.dispute_held_minor)}</div>}</td>
            <td data-label={t("Eligible for settlement")}>{amount(account.settle_available_minor)}{account.refund_pending && <div className="small muted">{t("Refund review pending")}</div>}</td>
            {operator && <td><button type="button" className="secondary" disabled={account.settle_available_minor === "0" || account.scale === null}
              onClick={() => setAction({ type: "lock", account, request: crypto.randomUUID(), currencies: data.currencies })}>{t(titles.lock)}</button></td>}
          </tr>;
        })}</WorkspaceTable> : <p className="empty muted">{t("No funds accounts are configured.")}</p>}
      </section>
      {!summaryOnly && !settlementInFinance && <SettlementRequests merchant={merchant} accounts={data.accounts} memberWrite={memberWrite} memberView={!merchant} operator={operator} refresh={state.refresh} token={token}/> }
      {!summaryOnly && merchant && <StatementImports merchant={merchant} routes={data.routes} operator={operator} refresh={state.refresh} focusDifferences={focusDifferences}/> }
      {!summaryOnly && merchant && <section className="workspace-panel">
        <div className="heading panel-heading"><h2 ref={bindingHeading} tabIndex={-1}>{t("Channel funds bindings")}</h2></div>
        {data.routes.length ? <WorkspaceTable label={t("Channel funds bindings")} columns={[
          t("Channel"), t("Direction"), t("Currency"), t("Funds account"), t("Settlement delay (days)"), ...(operator ? [t("Action")] : []),
        ]}>{data.routes.map(route => <tr key={[route.channel_id, route.direction, route.currency].join("/")}>
          <td>{route.channel_name}<div className="small muted">{route.channel_id}</div></td>
          <td>{direction(route.direction)}</td><td>{route.currency}</td><td>{route.account_id ?? "—"}</td>
          <td>{route.direction === "collection" ? route.maturity_days ?? "—" : "—"}</td>
          {operator && <td><button type="button" className="secondary" onClick={() => setAction({ type: "bind", route })}>{t(titles.bind)}</button></td>}
        </tr>)}</WorkspaceTable> : <p className="empty muted">{t("No channels assigned.")}</p>}
        {data.binding_history.length > 0 && <details>
          <summary>{t("Funds binding history")}</summary>
          <WorkspaceTable label={t("Funds binding history")} columns={[
            t("Channel"), t("Previous funds account"), t("Funds account"), t("Settlement delay (days)"), t("Operator"), t("Date"),
          ]}>{data.binding_history.map(change => <tr key={change.id}>
            <td>{change.channel_id} · {direction(change.direction)} · {change.currency}</td>
            <td>{change.previous_account_id ?? "—"}</td><td>{change.account_id}</td>
            <td>{change.direction === "collection" ? <>{change.previous_maturity_days ?? "—"} → {change.maturity_days}</> : "—"}</td>
            <td>{change.changed_by}</td><td>{dateTime(change.changed_at)}</td>
          </tr>)}</WorkspaceTable>
        </details>}
      </section>}
      {!summaryOnly && <section className="workspace-panel">
        <div className="heading panel-heading"><h2>{t("Funds settlement batches")}</h2></div>
        {data.batches.length ? <WorkspaceTable label={t("Funds settlement batches")} columns={[
          t("Funds account"), t("Original amount"), t("Transfer amount"), t("Status"), t("View details"), ...(operator ? [t("Action")] : []),
        ]}>{data.batches.map(batch => <tr key={batch.id}>
          <td>{batch.account_id}</td><td>{showAmount(batch.source_minor, batch.currency, batch.source_scale)}</td>
          <td>{batch.destination_amount} {batch.destination_currency}</td><td>{t(batchLabels[batch.state])}</td>
          <td><button type="button" className="text-button" onClick={() => setDetail(batch)}>{batch.transfer_reference ?? t("View details")}</button></td>
          {operator && <td>{batch.state === "locked" && <div className="actions">
            <button type="button" className="secondary" onClick={() => setAction({ type: "confirm", batch })}>{t(titles.confirm)}</button>
            <button type="button" className="secondary" onClick={() => setAction({ type: "cancel", batch })}>{t(titles.cancel)}</button>
          </div>}</td>}
        </tr>)}</WorkspaceTable> : <p className="empty muted">{t("No funds settlement batches.")}</p>}
      </section>}
      {!summaryOnly && merchant && <section className="workspace-panel">
        <div className="heading panel-heading"><h2>{t("Refund reconciliation")}</h2></div>
        {data.refunds.length ? <WorkspaceTable label={t("Refund reconciliation")} columns={[
          t("Statement entry"), t("Order"), t("Funds account"), t("Gross"), t("Statement fee"), t("Net"), t("Status"),
          t("Audit note"), ...(operator ? [t("Action")] : []),
        ]}>{data.refunds.map(row => {
          const account = data.accounts.find(item => item.id === row.account_id && item.currency === row.currency);
          const amount = (value: string) => showAmount(value, row.currency, account?.scale ?? null);
          return <tr key={[row.channel_id, row.currency, row.entry_id].join("/")}>
            <td>{row.entry_id}<div className="small muted">{row.channel_id} · {row.currency}</div>
              <div className="small muted">{row.batch_id} · {row.channel_reference}</div></td><td>{row.order_id ?? "—"}</td>
            <td>{row.account_id ?? "—"}</td><td>{amount(row.gross_minor)}</td><td>{amount(row.fee_minor)}</td>
            <td>{amount(row.net_minor)}</td><td>{t(refundLabels[row.review_status] ?? row.review_status)}</td>
            <td>{row.note ?? "—"}{row.applied_by && <div className="small muted">{row.applied_by}</div>}{row.recovery_note && <div>{row.recovery_note}</div>}{row.correction_id && <div>{t("Correcting entry")} {row.correction_id}</div>}{row.fee_return_minor != null && <div>{t("Merchant fee returned")} {amount(row.fee_return_minor)}</div>}</td>
            {operator && <td>{!["posted", "historical", "corrected", "recovered", "pending"].includes(row.review_status) && <button type="button" className="secondary"
              onClick={() => setAction({ type: "refund", refund: row })}>{t(titles.refund)}</button>}</td>}
          </tr>;
        })}</WorkspaceTable> : <p className="empty muted">{t("No statement refunds to review.")}</p>}
      </section>}
    </>}</LoadState>
    {history && <FundHistory {...history} merchant={merchant} token={token} openSource={openSource} close={()=>setHistory(undefined)} />}
    {detail && <Dialog drawer title={t("Settlement details")} close={() => setDetail(undefined)}>
      <button className="secondary" onClick={()=>{setHistory({account:detail.account_id,currency:detail.currency,scale:detail.source_scale,batch:detail.id});setDetail(undefined);}}>{t("Settlement source orders")}</button>
      <Details fields={[
        [t("Settlement ID"), detail.id], [t("Funds account"), detail.account_id],
        [t("Original amount"), showAmount(detail.source_minor, detail.currency, detail.source_scale)],
        [t("Exchange rate"), detail.fx_rate], [t("Transfer amount"), detail.destination_amount + " " + detail.destination_currency],
        [t("Status"), t(batchLabels[detail.state])], [t("Created"), dateTime(detail.created_at)],
        [t("Transfer reference"), detail.transfer_reference ?? "—"], [t("Paid on"), detail.paid_on ? date(detail.paid_on) : "—"],
        ...(detail.closed_at ? [[t("Closed at"), dateTime(detail.closed_at)] as [string, string]] : []),
        ...(detail.closed_by ? [[t("Operator"), detail.closed_by] as [string, string]] : []),
        ...(detail.cancel_reason ? [[t("Audit note"), detail.cancel_reason] as [string, string]] : []),
      ]} />
    </Dialog>}
    {merchant && action && <FundControl merchant={merchant} action={action} refresh={state.refresh} close={() => setAction(undefined)} />}
  </>;
}

function FundControl({ merchant, action, refresh, close }: { merchant: string; action: Action; refresh: () => void; close: () => void }) {
  const { t, exactMoney } = useLocale();
  return <ControlAction title={t(titles[action.type])} submitLabel={t(titles[action.type])} refresh={refresh} close={close}
    action={async fields => {
      const values = Object.fromEntries(fields);
      const scope = { action: action.type, merchant_id: merchant };
      switch (action.type) {
        case "bind":
          await productionControl("/funds", { ...scope, account_id: values.account_id, currency: action.route.currency,
            channel_id: action.route.channel_id, direction: action.route.direction, wait_upstream: values.wait_upstream === "on", maturity_days: Number(values.maturity_days ?? 0) });
          break;
        case "lock":
          await productionControl("/funds", { ...scope, destination_currency: values.destination_currency, fx_rate: values.fx_rate,
            source_minor: toMinor(String(values.source_amount), action.account.scale ?? 2),
            account_id: action.account.id, currency: action.account.currency, request_id: action.request });
          break;
        case "confirm": case "cancel":
          await productionControl("/funds", { ...scope, ...values, batch_id: action.batch.id });
          break;
        case "refund":
          await productionControl("/funds", { ...scope, ...values, entry_id: action.refund.entry_id,
            channel_id: action.refund.channel_id, currency: action.refund.currency });
          break;
      }
    }}>
    {action.type === "bind" && <>
      <p>{action.route.channel_name} · {action.route.currency} · {t(action.route.direction === "payout" ? "Payout" : "Collection")}</p>
      <label htmlFor="fund-account-id">{t("Funds account ID")}</label>
      <input id="fund-account-id" name="account_id" required pattern="[A-Za-z0-9.\x5f\x2d]+" maxLength={80}
        defaultValue={action.route.account_id ?? ""} />
      <p className="muted small">{t("Use the same funds account ID for channels that share a balance in this currency.")}</p>
      {action.route.direction === "collection" && <>
        <label><input name="wait_upstream" type="checkbox" defaultChecked={action.route.wait_upstream} />{t("Release for payout after upstream receipt")}</label>
        <label htmlFor="fund-maturity">{t("Fallback delay without channel cycle (days)")}</label>
        <input id="fund-maturity" name="maturity_days" type="number" required min="0" max="365" defaultValue={action.route.maturity_days ?? 0} />
      </>}
    </>}
    {action.type === "lock" && <>
      <p>{action.account.id} · {action.account.currency}</p>
      <p className="muted small">{t("Eligible for settlement")} {exactMoney(action.account.settle_available_minor, action.account.currency, action.account.scale ?? 2)}</p>
      <label htmlFor="fund-source">{t("Original amount")}</label>
      <input id="fund-source" name="source_amount" inputMode="decimal"
        pattern={action.account.scale ? `[0-9]+(\\.[0-9]{1,${action.account.scale}})?` : "[0-9]+"} maxLength={25} required />
      <label htmlFor="fund-destination">{t("Transfer currency")}</label>
      <select id="fund-destination" name="destination_currency" defaultValue={action.account.currency} required>
        {action.currencies.map(currency => <option key={currency} value={currency}>{currency}</option>)}
      </select>
      <label htmlFor="fund-exchange-rate">{t("Transfer currency units per original currency unit")}</label>
      <input id="fund-exchange-rate" name="fx_rate" inputMode="decimal" pattern="[0-9]+(\.[0-9]+)?" maxLength={25} required />
    </>}
    {(action.type === "confirm" || action.type === "cancel") && <p>{action.batch.id} · {action.batch.destination_amount} {action.batch.destination_currency}</p>}
    {action.type === "confirm" && <>
      <label htmlFor="fund-paid-currency">{t("Paid currency")}</label>
      <input id="fund-paid-currency" name="paid_currency" required maxLength={4} pattern="[A-Z]{3}|USDT" />
      <label htmlFor="fund-paid-amount">{t("Paid amount")}</label>
      <input id="fund-paid-amount" name="paid_amount" required inputMode="decimal" pattern="[0-9]+(\.[0-9]+)?" maxLength={29} />
      <label htmlFor="fund-transfer-reference">{t("Transfer reference")}</label>
      <input id="fund-transfer-reference" name="transfer_reference" required maxLength={120} />
      <label htmlFor="fund-paid-on">{t("Paid on")}</label>
      <input id="fund-paid-on" name="paid_on" type="date" required />
    </>}
    {action.type === "refund" && <>
      <p>{action.refund.entry_id} · {action.refund.order_id} · {action.refund.currency}</p>
      {!action.refund.order_id && <>
        <label htmlFor="fund-refund-order">{t("Original collection order ID")}</label>
        <input id="fund-refund-order" name="order_id" maxLength={120} />
      </>}
      {(action.refund.review_status !== "ready_to_apply" || action.refund.correction_candidate) && <>
        <label htmlFor="fund-correction-id">{t("Correcting entry ID")}</label>
        <input id="fund-correction-id" name="correction_id" maxLength={120} defaultValue={action.refund.correction_candidate ?? ""} />
      </>}
      <label htmlFor="fund-fee-return">{t("Merchant fee returned (minor units)")}</label>
      <input id="fund-fee-return" name="fee_return_minor" inputMode="numeric" pattern="[0-9]+" maxLength={12} required
        defaultValue={action.refund.correction_candidate ? "0" : undefined} />
    </>}
    {(action.type === "cancel" || action.type === "refund") && <>
      <label htmlFor="fund-reason">{t("Audit note")}</label>
      <textarea id="fund-reason" name="reason" required maxLength={300} rows={3} />
    </>}
  </ControlAction>;
}

function toMinor(value: string, scale: number) {
  const [whole, decimals = ""] = value.split(".");
  return (BigInt(whole) * 10n ** BigInt(scale) + BigInt(decimals.padEnd(scale, "0") || "0")).toString();
}
