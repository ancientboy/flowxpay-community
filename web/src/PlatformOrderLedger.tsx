import { PlatformOrderSummary } from "./PlatformOrderSummary";
import { PayoutFilterSelect } from "./PayoutFilterSelect";
import { ArrowUpRight, MagnifyingGlass } from "@phosphor-icons/react";
import { collectionStatuses, PaymentStatusBadge } from "./CollectionEvents";
import { useLocale } from "./locale";
import { payoutStatuses } from "./Payouts";
import { productionOperations } from "./platformEnvironment";
import type { PlatformOrder } from "./PlatformWorkspace";
import "./platform-payout-orders.css";

export const initialOrderFilters = { currency: "", date: "", status: "" };
type Filters = typeof initialOrderFilters;
type Props = {
  payout: boolean;
  orders: PlatformOrder[];
  merchants: { id: string; name: string }[];
  merchant: string;
  onMerchant: (id: string) => void;
  search: string;
  onSearchChange: (value: string) => void;
  reference: string;
  applySearch: (value: string) => void;
  riskFilter?: string;
  clearRisk: () => void;
  filters: Filters;
  setFilters: (filters: Filters) => void;
  openOrder: (order: PlatformOrder) => void;
};

const payoutTabs = [
  { status: "", label: "All payouts", color: "total" },
  { status: "succeeded", label: "Succeeded", color: "success" },
  { status: "pending_confirmation", label: "Pending confirmation", color: "pending" },
  { status: "failed", label: "Failed", color: "failed" },
];

function utcDay(timestamp: string) {
  return new Date(timestamp).toISOString().slice(0, 10);
}

export function PlatformOrderLedger({ payout, orders, merchants, merchant, onMerchant, search, onSearchChange,
  reference, applySearch, riskFilter, clearRisk, filters, setFilters, openOrder }: Props) {
  const { t, locale, exactMoney } = useLocale();
  const zh = locale === "zh-CN";
  const displayStatus = (order: PlatformOrder) => !payout && order.checkout_needs_review ? "review" : order.status;
  const scope = orders.filter(order =>
    (!filters.currency || filters.currency === order.currency) && (!filters.date || filters.date === utcDay(order.created_at)));
  const rows = scope.filter(order => !filters.status || filters.status === displayStatus(order));
  const statuses = payout ? payoutStatuses : collectionStatuses;
  const statusLabel = (value: string) => t(statuses[value] ?? value);
  const summaries = payout ? payoutTabs : [
    { status: "", label: "All collections", color: "total" },
    { status: "succeeded", label: "Paid", color: "success" },
    { status: "requires_action", label: "Awaiting payment", color: "pending" },
    { status: "review", label: "Payment review pending", color: "review" },
    { status: "failed", label: "Failed", color: "failed" },
  ];
  const merchantNames = new Map(merchants.map(item => [item.id, item.name]));
  const options = (values: string[], selected: string) => [...new Set([...values, selected].filter(Boolean))].sort();
  const change = (field: keyof Filters, value: string) => setFilters({ ...filters, [field]: value });
  function reset() {
    setFilters(initialOrderFilters);
    onSearchChange("");
    applySearch("");
    if (merchant) onMerchant("");
    if (riskFilter) clearRisk();
  }
  const hasFilters = Boolean(merchant || reference || search || filters.status || filters.currency || filters.date || riskFilter);
  return <div className="payout-book">
    <p className="payout-scope">{!payout ? t("Totals cover the collection orders returned by this query, by currency.") : zh ? "最多显示最近 50 笔代付，金额按币种汇总。" :
      "Summaries cover up to 50 recent orders returned by the query, with amounts shown per currency."}</p>
    {riskFilter && <div className="header-actions">
      <span>{t("Pending confirmation")} · {riskFilter}</span>
      <button className="secondary" type="button" onClick={clearRisk}>{t("Clear")}</button>
    </div>}
    <PlatformOrderSummary rows={rows} scope={scope.map(order => ({...order, status: displayStatus(order)}))} isoScale={productionOperations} statusLabel={statusLabel}
      label={(filters.status ? statusLabel(filters.status) + " · " : "") + t(payout ? "Payout volume" : "Collection order amount")} />
    <section className="payout-ledger" aria-labelledby="payout-list-title">
      <div className="payout-ledger-head"><h2 id="payout-list-title">{zh ? "订单明细" : "Order details"}</h2>
        <span role="status">{zh ? "共 " + rows.length + " 笔" : rows.length + " orders"}</span></div>
      <div className="payout-tabs" role="group" aria-label={t("Status")}>
        {summaries.map(({status, label, color}) => <button key={label} className={"payout-tab " + color} type="button"
          aria-pressed={filters.status === status} onClick={() => change("status", status)}>
          {t(label)} <span>{status ? scope.filter(o => displayStatus(o) === status).length : scope.length}</span>
        </button>)}
      </div>
      <form className="payout-tools" onSubmit={e => { e.preventDefault(); applySearch(search.trim()); }}>
        <label>{zh ? "订单号" : "Order reference"}
          <span className="payout-search-input"><MagnifyingGlass size={17} aria-hidden="true" />
            <input value={search} onChange={e => onSearchChange(e.target.value)} maxLength={80} id="ops-payment-reference"
              placeholder={t("Order reference or ID")} pattern={"[a-zA-Z0-9._:\\-]{1,80}"} /></span></label>
        <PayoutFilterSelect id="ops-merchant-scope" label={t("Merchant")} all={t("All merchants")}
          options={merchants.map(m => ({ value: m.id, label: m.name + " · " + m.id }))} value={merchant} onChange={onMerchant} />
        <PayoutFilterSelect id="ops-payout-currency" label={t("Currency")} all={zh ? "全部币种" : "All currencies"}
          options={options(orders.map(o => o.currency), filters.currency).map(value => ({ value, label: value }))}
          value={filters.currency} onChange={value => change("currency", value)} />
        <PayoutFilterSelect id="ops-payout-date" label={zh ? "创建日期 · UTC" : "Created · UTC"} all={zh ? "全部日期" : "All dates"}
          options={options(orders.map(o => utcDay(o.created_at)), filters.date).reverse().map(value => ({ value, label: value }))}
          value={filters.date} onChange={value => change("date", value)} />
        <PayoutFilterSelect id="ops-payout-status" label={t("Status")} all={zh ? "全部状态" : "All statuses"}
          options={options(orders.map(displayStatus), filters.status).map(value => ({ value, label: statusLabel(value) }))}
          value={filters.status} onChange={value => change("status", value)} />
        <div className="payout-filter-actions"><button type="submit" className="primary">{t("Search")}</button>
          <button type="button" className="text-button" disabled={!hasFilters} onClick={reset}>{zh ? "重置" : "Reset"}</button></div>
      </form>
      {rows.length ? <table className="payout-table"><thead><tr>
        <th scope="col">{t("Merchant")}</th><th scope="col">{t("Platform reference")}</th>
        <th scope="col" className="amount">{t("Amount")}</th><th scope="col">{t("Status")}</th>
        <th scope="col">{zh ? "创建时间 · UTC" : "Created · UTC"}</th><th scope="col"><span className="payout-sr">{t(payout ? "Payout details" : "Payment details")}</span></th>
      </tr></thead><tbody>{rows.map(order => {
        const timestamp = new Date(order.created_at).toISOString();
        return <tr key={order.id}>
          <td className="merchant"><div className="payout-merchant">
            <span className="merchant-initial" aria-hidden="true">{(merchantNames.get(order.merchant) ?? order.merchant).slice(0,1)}</span>
            <div><button className="text-button" onClick={() => openOrder(order)}>{merchantNames.get(order.merchant) ?? order.merchant}</button>
              <small>{order.reference || order.merchant}</small></div></div></td>
          <td className="ref"><button className="text-button" onClick={() => openOrder(order)}>{order.id}</button><small>{order.merchant}</small></td>
          <td className="amount">{exactMoney(String(order.amount_minor), order.currency, productionOperations)}</td>
          <td className="status"><PaymentStatusBadge payout={payout} payment={order} /></td>
          <td className="created"><time dateTime={order.created_at}>{timestamp.slice(0, 10)}<small>{timestamp.slice(11, 19)} UTC</small></time></td>
          <td className="inspect"><button type="button" aria-label={t(payout ? "Payout details" : "Payment details") + " · " + order.id} onClick={() => openOrder(order)}>
            <ArrowUpRight size={17} /></button></td>
        </tr>;
      })}</tbody></table> : <p className="payout-empty">{t(payout ? "No payouts match your search." : "No payments match your search.")}</p>}
    </section>
  </div>;
}
