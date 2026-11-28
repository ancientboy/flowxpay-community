import { HandlingNote, OrderTime, useOrderClock } from "./OrderContext";
import { PlatformOrderSummary, matchesOrderStatus } from "./PlatformOrderSummary";
import { PeriodReport } from "./PeriodReport";
import { MerchantPaymentFunds } from "./MerchantPaymentFunds";
import { memberPortal } from "./memberEnvironment";
import type { WorkspaceCapabilities } from "./memberEnvironment";
import { MerchantRates, OrderQuote } from "./ChannelPricing";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { ArrowRight, Bank, ChartLineUp, CreditCard, Receipt } from "@phosphor-icons/react";
import { type Payment } from "./api";
import { PaymentStatusBadge } from "./CollectionEvents";
import { SiteCards, SiteExplanation, type Site, type SiteCard } from "./SiteCards";
import { readSettlement, SettlementConfirmations } from "./Settlement";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";
import { base } from "./WorkspaceAccess";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

const currencies = ["USD", "BRL", "INR", "THB"] as const;
type Currency = typeof currencies[number];
export interface WorkspaceSetup {
  recent_currency: Currency | null;
  open_currencies: Currency[];
  effective_account_ids: string[];
  sites: (Site | SiteCard)[];
  collection_currencies?: Currency[];
}

interface OverviewTotals {
  orders: number;
  paid_orders: number;
  gross_minor: string;
  fees_minor: string | null;
  settled_net_minor: string | null;
}

export function WorkspaceOverview({ capabilities, token, openChannels, openPayments, openSettlement, openSite, openPendingPayouts }: {
  capabilities: WorkspaceCapabilities;
  token: string;
  openChannels: () => void;
  openPayments: () => void;
  openSettlement: (account?: string) => void;
  openPendingPayouts?: () => void;
  openSite: (site: Site | SiteCard) => void;
}) {
  const {t} = useLocale();
  const [currency,setCurrency] = useState<Currency>();
  const [days,setDays] = useState("7");
  const setup = useWorkspaceData<WorkspaceSetup>(base + "/setup", token);
  const availableCurrencies = setup.data?.collection_currencies ?? (capabilities.siteRouting ? currencies.filter(value => value !== "THB") : []);
  const selected = currency ?? setup.data?.recent_currency ?? setup.data?.open_currencies[0] ?? availableCurrencies[0];
  const payouts = useWorkspaceData<{status:string}[]>(openPendingPayouts && capabilities.payoutWorkflow ? "/v1/payouts" : undefined, token);
  const pending = payouts.data?.filter(row => row.status === "pending_confirmation").length;
  return <div className="merchant-overview">
    {Boolean(pending) && <section className="workspace-panel merchant-home-tasks">
      <h2>{t("Pending confirmation")}</h2>
      <button type="button" className="text-button" onClick={openPendingPayouts}>{t("Payout orders")} · {pending}</button>
    </section>}
    {!memberPortal && capabilities.paymentDemos && <LoadState state={setup} label={t("Collection setup")}>{data => !data.recent_currency && <section className="workspace-panel overview-summary">
      <div className="heading panel-heading"><h2>{t("Get started with sandbox collections")}</h2></div>
      <div className="overview-footer">
        <p className="muted">{t(data.open_currencies.length
          ? "Review the payment methods available for your sites before your first test order."
          : "Platform operations will enable a payment method for your site. Check back after they complete setup.")}</p>
        {data.open_currencies.length > 0 && <button className="primary" onClick={openChannels}>{t("View available payment methods")}</button>}
      </div>
    </section>}</LoadState>}
    <section className="workspace-panel overview-summary" aria-label={t("Collection and settlement summary")}>
      <div className="heading panel-heading">
        <h2>{t("Collection and settlement summary")}</h2>
        {selected && <div className="overview-filters">
          <select aria-label={t("Currency")} value={selected} onChange={event => setCurrency(event.target.value as Currency)}>
            {[...new Set([...availableCurrencies, selected])].map(value => <option key={value}>{value}</option>)}
          </select>
          <select aria-label={t("Reporting period")} value={days} onChange={event => setDays(event.target.value)}>
            <option value="7">{t("Last 7 days")}</option><option value="30">{t("Last 30 days")}</option>
          </select>
        </div>}
      </div>
      {selected && (setup.data || setup.error || currency) && <OverviewMetrics key={selected + days} token={token} currency={selected} days={days} reconciliation={capabilities.statementReconciliation} />}
      {!selected && <LoadState state={setup} label={t("Collection setup")}>{() =>
        <p className="muted empty">{t("No collection currencies are assigned or recorded.")}</p>}</LoadState>}
      <div className="overview-footer">
        <p className="small muted">{t(capabilities.statementReconciliation
          ? "Collections use order date. Settlement totals use settled statements by statement date. All dates are UTC."
          : "Collection totals use the order date in UTC. Statement-based amounts are unavailable.")}</p>
        <button className="text-button panel-link" onClick={() => openSettlement()}>{t("View settlement")}<ArrowRight size={16} aria-hidden="true" /></button>
      </div>
    </section>
    {memberPortal && <PeriodReport endpoint={base + "/period-report"} token={token} />}
    {capabilities.siteRouting && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Merchant sites")}</h2></div>
      <SiteExplanation />
      <LoadState state={setup} label={t("sites")}>{data =>
        <SiteCards sites={data.sites} open={openSite} action="View site details" />}</LoadState>
    </section>}
    {!capabilities.siteRouting && <MerchantRates token={token} siteRouting={capabilities.siteRouting} />}
    {capabilities.settlementConfirmations && <SettlementConfirmations token={token} />}
    {capabilities.statementReconciliation && <SettlementSummary token={token} open={openSettlement} />}
    <WorkspacePayments token={token} openPayments={openPayments} />
  </div>;
}

interface SettlementCurrency {
  currency: Currency;
  pending_net_minor: string | null;
  settled_net_minor: string | null;
  latest_settled_statement_date: string | null;
  statement_account_id: string;
}

function SettlementSummary({ token, open }: { token: string; open: (account?: string) => void }) {
  const { t, exactMoney, date } = useLocale();
  const state = useWorkspaceData<SettlementCurrency[]>(base + "/settlement-summary", token, readSettlement);
  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t("Settlement by currency")}</h2>
      <button className="text-button panel-link" onClick={() => open()}>{t("View statement batches")}<ArrowRight size={16} aria-hidden="true" /></button>
    </div>
    <p className="muted panel-note">{t("All imported statements by currency. Reported net amounts reflect the statements and are separate from collection volume and payout balances.")}</p>
    <LoadState state={state} label={t("Settlement by currency")}>{rows => rows.length
      ? <ul className="platform-card-grid platform-merchant-cards">{rows.map(row => <li className="platform-card" key={row.currency}>
        <div className="platform-card-identity"><h3>{row.currency}</h3></div>
        <dl className="platform-card-meta settlement-card-meta">
          <div><dt>{t("Pending settlement net")}</dt><dd>{row.pending_net_minor === null ? t("No amount recorded") : exactMoney(row.pending_net_minor, row.currency)}</dd></div>
          <div><dt>{t("Settled net amount")}</dt><dd>{row.settled_net_minor === null ? t("No amount recorded") : exactMoney(row.settled_net_minor, row.currency)}</dd></div>
          <div><dt>{t("Latest settled statement date")}</dt><dd>{row.latest_settled_statement_date ? date(row.latest_settled_statement_date) : t("No settled statements")}</dd></div>
        </dl>
        <button className="text-button panel-link platform-card-link" onClick={() => open(row.statement_account_id)}>
          {t("View {currency} statements", { currency: row.currency })}<ArrowRight size={16} aria-hidden="true" />
        </button>
      </li>)}</ul> : <p className="muted empty">{t("No settlement statements have been imported.")}</p>}</LoadState>
  </section>;
}

function OverviewMetrics({ token, currency, days, reconciliation }: { token: string; currency: string; days: string; reconciliation: boolean }) {
  const { t, exactMoney, locale } = useLocale();
  const state = useWorkspaceData<OverviewTotals>(base + "/overview?" + new URLSearchParams({ currency, days }), token);
  const percent = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 1 });
  return <LoadState state={state} label={t("overview")}>{data => <div className="overview-metrics platform-metrics">
    <div><span><ChartLineUp size={19} aria-hidden="true" />{t("Successful collection volume")}</span><strong>{exactMoney(data.gross_minor, currency)}</strong></div>
    <div><span><CreditCard size={19} aria-hidden="true" />{t("Paid orders / total orders")}</span><strong>{data.paid_orders} / {data.orders}</strong>
      {data.orders > 0 && <small>{t("Success rate")} · {percent.format(data.paid_orders / data.orders)}</small>}</div>
    <div><span><Receipt size={19} aria-hidden="true" />{t("Settled statement fees")}</span><strong>{data.fees_minor === null ? "—" : exactMoney(data.fees_minor, currency)}</strong></div>
    <div><span><Bank size={19} aria-hidden="true" />{t("Settled net amount")}</span><strong>{data.settled_net_minor === null ? "—" : exactMoney(data.settled_net_minor, currency)}</strong>
      {reconciliation && data.settled_net_minor === null && <small>{t("No settled statements for this period")}</small>}</div>
  </div>}</LoadState>;
}

export function WorkspacePayments({ token, openPayments, initialOrder = "" }: { token: string; openPayments?: () => void; initialOrder?: string }) {
  const { t } = useLocale();
  const [reference, setReference] = useState(initialOrder);
  const [query, setQuery] = useState(initialOrder);
  const autoOpen = useRef(Boolean(initialOrder));
  const recent = Boolean(openPayments);
  function search(event: FormEvent) {
    event.preventDefault();
    autoOpen.current = false;
    setQuery(reference.trim());
  }
  const heading = <div className="heading panel-heading">
      <h2>{t(recent ? "Recent transactions" : "Payment records")}</h2>
      {openPayments && <button className="text-button panel-link" onClick={openPayments}>{t("View all payments")}<ArrowRight size={16} aria-hidden="true" /></button>}
      {!recent && <form className="overview-filters payment-reference-filter" onSubmit={search}>
        <input aria-label={t("Search payments")} placeholder={t("Order reference or ID")} value={reference}
          onChange={event => setReference(event.target.value)} maxLength={160} />
        <button className="secondary">{t("Search")}</button>
        {query && <button className="secondary" type="button" onClick={() => { autoOpen.current = false; setReference(""); setQuery(""); }}>{t("Clear")}</button>}
      </form>}
    </div>;
  return <WorkspacePaymentRows key={query} token={token} recent={recent} query={query} heading={heading} autoOpen={autoOpen.current} />;
}

function WorkspacePaymentRows({ token, recent, query, autoOpen, heading }: { token: string; recent: boolean; query: string; autoOpen: boolean; heading: React.ReactNode }) {
  const { t, money, dateTime, localizedLink } = useLocale();
  const now = useOrderClock();
  const [selected, setSelected] = useState<Payment>();
  const [status, setStatus] = useState("");
  const params = new URLSearchParams(recent ? { recent: "1" } : { reference: query });
  const state = useWorkspaceData<Payment[]>(base + "/payments?" + params, token);
  const opened = useRef(false);
  useEffect(() => {
    if (!autoOpen || opened.current || !state.data) return;
    opened.current = true;
    setSelected(state.data.find(row => row.id === query));
  }, [autoOpen, query, state.data]);
  return <>
    {!recent && state.data && !state.error && <PlatformOrderSummary rows={state.data} selected={status} onSelect={setStatus} isoScale={false} />}
    <section className="workspace-panel member-payments">{heading}
    <LoadState state={state} label={t("payments")}>{items => {
      const rows = items.filter(row => matchesOrderStatus(row, status));
      return rows.length ?
      <WorkspaceTable label={t("Payment list")} columns={[t("Reference"), t("Amount"), t("Status"), t("Created")]}>
        {rows.map(payment => <tr key={payment.id}>
          <td><button className="text-button" onClick={() => setSelected(payment)}>{payment.merchant_reference}</button></td>
          <td className="payment-amount">{money(payment.amount_minor, payment.currency)}</td>
          <td><PaymentStatusBadge payment={payment} /><HandlingNote record={payment} /></td>
          <td><OrderTime at={payment.created_at} now={now} /></td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("No payments match your search.")}</p>;}}</LoadState>
    </section>
    {selected && <Dialog drawer title={t("Payment details")} close={() => setSelected(undefined)}>
      {memberPortal && <MerchantPaymentFunds key={selected.id} id={selected.id} token={token} />}
      <Details fields={[
        [t("Merchant reference"), selected.merchant_reference],
        [t("Platform reference"), selected.id],
        [t("Amount"), money(selected.amount_minor, selected.currency)],
        [t("Quoted merchant fee"), <OrderQuote quote={selected.quote} currency={selected.currency} />],
        ...(!memberPortal ? [[t("Status"), <PaymentStatusBadge payment={selected} />] as [string, React.ReactNode]] : []),
        [t("Created"), dateTime(selected.created_at)],
      ]} />
      {selected.checkout_needs_review && <p className="panel-note" role="status">{t("The payment code has expired. We are checking the original order.")}</p>}
      {selected.checkout_path && <a className="primary" href={localizedLink(selected.checkout_path)}
        target="_blank" rel="noreferrer">{t("Open checkout")}</a>}
    </Dialog>}
  </>;
}
