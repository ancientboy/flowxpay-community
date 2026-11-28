import { PeriodReport } from "./PeriodReport";
import {MerchantEvents} from "./MerchantEvents";
import {DispatchControls} from "./DispatchControls";
import {SettlementRequests} from "./SettlementRequests";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useLocale } from "./locale";
import { LoadState, WorkspaceTable, useWorkspaceData } from "./WorkspaceInventoryUI";
import { ProductionFunds } from "./ProductionFunds";
import { ProductionSettlements } from "./ProductionPlatformControls";
import { platformAPI, productionOperations } from "./platformEnvironment";
import type { PlatformSession } from "./PlatformWorkspace";

interface FinanceAmount {
  currency: string; direction: "collection" | "payout"; orders: number; succeeded: number; in_progress: number;
  successful_minor: string; priced?: number; estimated_revenue_minor?: string; estimated_cost_minor?: string;
  estimated_margin_minor?: string; matched_cost_orders?: number; actual_channel_cost_minor?: string; matched_margin_minor?: string;
}
interface FinancePayout {
  id: string; merchant_id: string; merchant: string; merchant_reference: string; currency: string;
  amount_minor: string; status: string; created_at: string;
}
interface FinanceBatch {
  id: string; merchant_id: string; merchant: string; account_id: string; currency: string; statement_date: string;
  settlement: string; withdrawal: string; upstream_received_minor?: string; upstream_confirmed_rows?: number; differences: number; rows: number;
  statement_gross_minor: string; statement_fee_minor: string; statement_net_minor: string;
}
interface FinanceReport {
  from: string; to: string; pending_settlements?: number; reconciliation_work?: {merchant_id:string;name:string;differences:number}[]; summary: FinanceAmount[]; payouts: FinancePayout[]; settlements: FinanceBatch[];
}
const statusLabels: Record<string, string> = {
  queued: "Queued", pending_approval: "Pending approval", routing: "Processing", cancelled: "Cancelled", expired: "Expired",
  draft: "Draft", processing: "Processing", pending_confirmation: "Pending confirmation",
  succeeded: "Succeeded", failed: "Failed", rejected: "Rejected", pending: "Pending", settled: "Settled",
  not_reported: "Not reported", completed: "Completed",
};

export function PlatformFinance({ session, merchant, focusBindings, focusDifferences, selectMerchant, openOrder }: {
  session: PlatformSession; merchant: string; focusBindings?: boolean; focusDifferences?: boolean;
  selectMerchant: (merchant: string, differences?: boolean) => void; openOrder: (id: string) => void;
}) {
  const { t, exactMoney, dateTime, locale } = useLocale();
  const zh = locale === "zh-CN";
  const [pendingOnly, setPendingOnly] = useState(false);
  const [openDifferences, setOpenDifferences] = useState(Boolean(focusDifferences));
  const reconciliationHeading = useRef<HTMLHeadingElement>(null);
  const requestHeading = useRef<HTMLDivElement>(null);
  const [dates, setDates] = useState({ from: "", to: "" });
  const query = new URLSearchParams({ merchant, ...dates });
  const state = useWorkspaceData<FinanceReport>(platformAPI + "/finance?" + query, session.token);
  const reportLoaded = Boolean(state.data);
  useEffect(() => {
    if (reportLoaded && focusDifferences && !merchant) reconciliationHeading.current?.focus();
  }, [reportLoaded, focusDifferences, merchant]);
  const operator = session.role === "operator";
  const status = (value: string) => t(statusLabels[value] ?? value);
  const amount = (value: string | undefined, currency: string) => value === undefined ? "—" : exactMoney(value, currency, productionOperations);
  function search(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setDates({ from: String(data.get("from") ?? ""), to: String(data.get("to") ?? "") });
  }

  return <div className="merchant-overview">
    {!productionOperations && <SettlementRequests sandbox merchant={merchant} operator={operator} token={session.token} refresh={()=>{}}/>}
    {productionOperations && <LoadState state={state} label={zh ? "财务待办" : "Finance work queue"}>{report =>
      <section className="workspace-panel">
        <div className="heading panel-heading"><h2 ref={reconciliationHeading} tabIndex={-1}>{zh ? "财务待办" : "Finance work queue"}</h2></div>
        <div className="header-actions">
          <button className="secondary" onClick={() => {setPendingOnly(true);requestHeading.current?.focus()}}>
            {zh ? "待处理结算" : "Settlements requiring action"} · {report.pending_settlements ?? 0}
          </button>
          <button className="secondary" aria-expanded={openDifferences} onClick={() => setOpenDifferences(!openDifferences)}>
            {zh ? "未处理对账差异" : "Open statement differences"} · {report.reconciliation_work?.reduce((sum,row)=>sum+row.differences,0) ?? 0}
          </button>
        </div>
        {openDifferences && (report.reconciliation_work?.length ? <ul>
          {report.reconciliation_work.map(row=><li key={row.merchant_id}>
            <button className="text-button" onClick={() => {
              if (merchant === row.merchant_id) document.querySelector<HTMLElement>("#upstream-statement-heading")?.focus();
              else selectMerchant(row.merchant_id,true);
            }}>{row.name} · {row.differences}</button>
          </li>)}
        </ul> : <p>{zh ? "暂无 CSV 账单差异。到期未对平订单请核对上游账单是否已导入。" : "No open CSV discrepancies. For unreconciled orders, check whether the upstream statement has been imported."}</p>)}
      </section>
    }</LoadState>}
    {productionOperations && <PeriodReport endpoint={platformAPI + "/period-report"} token={session.token} merchant={merchant} />}
    {productionOperations && <div ref={requestHeading} tabIndex={-1}>
      {pendingOnly && <button className="secondary" onClick={()=>setPendingOnly(false)}>{zh ? "查看全部结算申请" : "Show all settlement requests"}</button>}
      <SettlementRequests key={String(pendingOnly)} merchant={merchant || undefined} operator={operator} pending={pendingOnly} token={session.token} refresh={state.refresh} />
    </div>}
    <section className="workspace-panel platform-control-panel">
      <div className="heading panel-heading"><h2>{t("Platform performance")}</h2></div>
      <form className="platform-finance-filters" onSubmit={search}>
        <label>{t("Start date")} <input type="date" name="from" /></label>
        <label>{t("End date")} <input type="date" name="to" /></label>
        <button className="secondary">{t("Search")}</button>
      </form>
      <p className="muted panel-note">{t("Dates use UTC. The default range is seven days, and a report may cover up to 31 days.")}</p>
    </section>
    <LoadState state={state} label={t("Platform finance")}>{report => <>
      <section className="workspace-panel platform-control-panel">
        <div className="heading panel-heading"><h2>{t("Volume and estimated gross margin")}</h2>
          <span className="muted small">{report.from} – {report.to} UTC</span></div>
        <p className="muted panel-note">{t("Volume is grouped by currency and order direction. Estimated margins include only successful orders with both prices recorded.")}</p>
        {report.summary.length ? <WorkspaceTable label={t("Platform performance")} columns={[
          t("Direction"), t("Currency"), t("Orders"), t("In progress"), t("Successful orders"), t("Successful volume"),
          ...(operator ? [t("Priced orders"), t("Quoted merchant fee"), t("Estimated channel cost"), t("Estimated gross margin"), ...(productionOperations ? [t("Matched cost orders"), t("Actual channel cost"), t("Matched order margin")] : [])] : []),
        ]}>{report.summary.map(row => <tr key={row.currency + row.direction}>
          <td>{t(row.direction === "collection" ? "Collection" : "Payout")}</td><td>{row.currency}</td>
          <td>{row.orders}</td><td>{row.in_progress}</td><td>{row.succeeded}</td><td>{amount(row.successful_minor, row.currency)}</td>
          {operator && <><td>{row.priced} / {row.succeeded}</td>
            <td>{amount(row.estimated_revenue_minor, row.currency)}</td><td>{amount(row.estimated_cost_minor, row.currency)}</td>
            <td>{amount(row.estimated_margin_minor, row.currency)}</td>
            {productionOperations && <><td>{row.matched_cost_orders} / {row.succeeded}</td>
              <td>{amount(row.actual_channel_cost_minor, row.currency)}</td><td>{amount(row.matched_margin_minor, row.currency)}</td></>}</>}
        </tr>)}</WorkspaceTable> : <p className="muted empty">{t("No transactions in this date range.")}</p>}
      </section>
      <section className="workspace-panel platform-control-panel">
        <div className="heading panel-heading"><h2>{t("Platform payout orders")}</h2><span className="muted small">{t("Latest 100 records")}</span></div>
        {report.payouts.length ? <WorkspaceTable label={t("Platform payout orders")} columns={[
          t("Merchant"), t("Platform reference"), t("Merchant reference"), t("Amount"), t("Status"), t("Created"),
        ]}>{report.payouts.map(row => <tr key={row.id}>
          <td>{row.merchant}<div className="muted small">{row.merchant_id}</div></td>
          <td className="order-number">{productionOperations ? <button type="button" className="text-button wrap" onClick={() => openOrder(row.id)}>{row.id}</button> : row.id}</td><td>{row.merchant_reference}</td>
          <td>{amount(row.amount_minor, row.currency)}</td><td>{status(row.status)}</td><td>{dateTime(row.created_at)}</td>
        </tr>)}</WorkspaceTable> : <p className="muted empty">{t("No payout orders in this date range.")}</p>}
      </section>
      <section className="workspace-panel platform-control-panel">
        <div className="heading panel-heading"><h2>{t("Merchant settlement batches")}</h2><span className="muted small">{t("Latest 100 records")}</span></div>
        <p className="muted panel-note">{t("Matched order margin excludes refunds, FX gains or losses and orders without attributed costs.")}</p>
        {report.settlements.length ? <WorkspaceTable label={t("Merchant settlement batches")} columns={[
          t("Merchant"), t("Batch"), t("Statement date"), t("Settlement"), productionOperations ? (zh ? "上游结算确认" : "Upstream settlement confirmation") : t("Withdrawal status"),
          t("Statement gross"), t("Statement fee"), t("Statement net"), t("Reconciliation differences"),
        ]}>{report.settlements.map(row => <tr key={[row.merchant_id,row.account_id,row.currency,row.id].join(":")}>
          <td>{row.merchant}<div className="muted small">{row.merchant_id}</div></td>
          <td>{row.id}<div className="muted small">{row.currency} · {row.account_id}</div></td>
          <td>{row.statement_date}</td><td>{status(row.settlement)}</td><td>{productionOperations ? (row.upstream_confirmed_rows ?
            <>{amount(row.upstream_received_minor, row.currency)}<div className="muted small">{row.upstream_confirmed_rows} / {row.rows}</div></> :
            (zh ? "待凭证确认" : "Awaiting receipt evidence")) : status(row.withdrawal)}</td>
          <td>{amount(row.statement_gross_minor, row.currency)}</td><td>{amount(row.statement_fee_minor, row.currency)}</td>
          <td>{amount(row.statement_net_minor, row.currency)}</td><td>{row.differences} / {row.rows}</td>
        </tr>)}</WorkspaceTable> : <p className="muted empty">{t("No settlement batches in this date range.")}</p>}
      </section>
    </>}</LoadState>
    {productionOperations && <DispatchControls operator={operator} token={session.token} openOrder={openOrder}/>}
    {productionOperations && (merchant ? <div className="merchant-overview" key={merchant}>
      <MerchantEvents merchant={merchant} token={session.token} write={operator} openOrder={openOrder}/>
      <ProductionFunds token={session.token} merchant={merchant} operator={operator} focusBindings={focusBindings} focusDifferences={focusDifferences} settlementInFinance openOrder={openOrder} />
      <ProductionSettlements merchant={merchant} />
    </div>
      : <p className="muted">{t("Select a merchant to view offline settlement confirmations.")}</p>)}
  </div>;
}
