import { useLocale } from "./locale";

type SummaryRow = { currency: string; amount_minor: string | number; status: string };

function amountsByCurrency(orders: SummaryRow[]) {
  const sums = new Map<string, bigint>();
  for (const order of orders) {
    sums.set(order.currency, (sums.get(order.currency) ?? 0n) + BigInt(order.amount_minor));
  }
  return [...sums].sort(([a], [b]) => a.localeCompare(b));
}

export function PlatformOrderSummary({ rows, scope, label, statusLabel, isoScale }: {
  rows: SummaryRow[]; scope: SummaryRow[]; label: string; statusLabel: (status: string) => string; isoScale: boolean;
}) {
  const { locale, exactMoney } = useLocale();
  const zh = locale === "zh-CN";
  const totals = amountsByCurrency(rows);
  const counts = new Map<string, number>();
  for (const { status } of scope) counts.set(status, (counts.get(status) ?? 0) + 1);
  const states = [...counts];
  return <section className="payout-summary" aria-label={zh ? "当前筛选汇总" : "Filtered summary"}>
      <div className="payout-funds">
        <h2>{label}</h2>
        <div className="payout-denominations">{totals.length ? totals.map(([currency, amount]) =>
          <div key={currency}><span>{currency}</span><strong>{exactMoney(String(amount), currency, isoScale).replace(currency, "").trim()}</strong></div>
        ) : <strong>—</strong>}</div>
      </div>
      <div className="payout-activity"><h2>{zh ? "订单分布" : "Order breakdown"} <small>{scope.length}</small></h2>
        <div className="payout-distribution" aria-hidden="true">{states.map(([status, count]) =>
          <span key={status} data-status={status} style={{flex: count}} />)}</div>
        <div className="payout-legend">{states.map(([status, count]) => <span key={status}><i data-status={status} />
          {statusLabel(status)} <b>{count}</b></span>)}</div>
      </div>
    </section>;
}
