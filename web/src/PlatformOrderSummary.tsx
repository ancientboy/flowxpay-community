import { CheckCircle, Clock, MagnifyingGlass, Stack, WarningCircle } from "@phosphor-icons/react";
import { useLocale } from "./locale";

type SummaryRow = { currency: string; amount_minor: string | number; status: string; checkout_needs_review?: boolean };

export function PlatformOrderSummary({ rows, payout = false, selected, onSelect, isoScale }: {
  rows: SummaryRow[]; payout?: boolean; selected: string; onSelect: (status: string) => void; isoScale: boolean;
}) {
  const { locale, t, exactMoney } = useLocale();
  const cards = [
    { status: "", label: payout ? "All payouts" : "All collections", icon: Stack, color: "total" },
    { status: "succeeded", label: payout ? "Succeeded" : "Paid", icon: CheckCircle, color: "success" },
    { status: payout ? "pending_confirmation" : "requires_action", label: payout ? "Pending confirmation" : "Awaiting payment", icon: Clock, color: "pending" },
    ...(payout ? [] : [{ status: "review", label: "Payment review pending", icon: MagnifyingGlass, color: "review" }]),
    { status: "failed", label: "Failed", icon: WarningCircle, color: "failed" },
  ];
  return <div className="payout-metrics" role="group" aria-label={t("Status")}>
    {cards.map(({ status, label, icon: Icon, color }) => {
      const matches = rows.filter(row => !status || status === (!payout && row.checkout_needs_review ? "review" : row.status));
      const sums = new Map<string, bigint>();
      for (const row of matches) {
        sums.set(row.currency, (sums.get(row.currency) ?? 0n) + BigInt(row.amount_minor));
      }
      return <button type="button" key={label} className={"payout-metric " + color}
        aria-pressed={selected === status} onClick={() => onSelect(status)}>
        <span className="payout-metric-top"><span className="payout-icon"><Icon size={19} aria-hidden="true" /></span>
          <span>{t(label)}</span><span className="payout-count">{matches.length}{locale === "zh-CN" ? " 笔" : " orders"}</span></span>
        <span className="payout-values">{sums.size ? [...sums].sort(([a], [b]) => a.localeCompare(b)).map(([currency, amount]) =>
          <strong key={currency}>{exactMoney(amount.toString(), currency, isoScale)}</strong>) : <strong>—</strong>}</span>
      </button>;
    })}
  </div>;
}
