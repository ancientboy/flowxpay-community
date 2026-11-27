import { PaymentStatusBadge } from "./CollectionEvents";
import { useLocale } from "./locale";
import { base } from "./WorkspaceAccess";
import { Details, LoadState, useWorkspaceData } from "./WorkspaceInventoryUI";

type PaymentFunds = {
  payment_status: string; checkout_needs_review?: boolean; account_id: string | null; currency: string; scale: number | null;
  credited_minor: string | null; account_available_minor: string | null;
  due_at: string | null; due_reached: boolean | null; statement_matched: boolean;
  receipt_recorded: boolean; receipt_required: boolean; open_difference: boolean;
  settled_minor: string | null;
  settlements: { id: string; state: "locked" | "confirmed"; allocated_minor: string; transfer_reference: string | null; paid_on: string | null }[];
};

export function MerchantPaymentFunds({ id, token }: { id: string; token: string }) {
  const { locale, exactMoney, date, dateTime, t } = useLocale();
  const l = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const title = l("这笔收款的资金去向", "Where these funds are");
  const state = useWorkspaceData<PaymentFunds>(base + "/payment-funds?" + new URLSearchParams({ order_id: id }), token);
  return <section aria-label={title} className="merchant-payment-funds">
    <div className="heading"><h3>{title}</h3><button className="secondary" type="button" onClick={state.reload}>{t("Refresh status")}</button></div>
    <LoadState state={state} label={title}>{funds => {
      const money = (value: string) => funds.scale === null
        ? value + " " + funds.currency + " · " + t("minor units")
        : exactMoney(value, funds.currency, funds.scale);
      const credited = funds.credited_minor;
      const settled = funds.settled_minor;
      const complete = credited !== null && settled !== null && BigInt(settled) >= BigInt(credited);
      let availability = l("等待付款结果", "Awaiting payment result");
      let settlement = availability;
      if (funds.payment_status === "succeeded") {
        availability = credited === null ? l("暂无这笔入账记录", "No credit recorded for this order") :
          l("已入账 · 实际可代付额度按账户计算", "Credited · payout availability is calculated for the account");
        if (complete) settlement = l("出款已登记", "Transfer recorded");
        else if (settled !== null) settlement = l("部分出款已登记", "Partial transfer recorded");
        else if (funds.settlements.some(batch => batch.state === "locked")) settlement = l("已分配到结算批次", "Allocated to a settlement batch");
        else if (credited === null) settlement = l("暂无资金入账凭据", "No credit evidence is available");
        else if (funds.open_difference) settlement = l("账单差异待核对", "Statement discrepancy requires review");
        else if (!funds.statement_matched) settlement = l("待匹配上游账单", "Awaiting a matching upstream statement");
        else if (!funds.due_reached) settlement = l("待到结算日", "Awaiting settlement maturity");
        else if (funds.receipt_required && !funds.receipt_recorded) settlement = l("待上游结算确认", "Awaiting upstream settlement confirmation");
        else settlement = l("已对账并确认上游结算 · 可申请金额以结算页为准", "Reconciled and received upstream · see Settlement for the available amount");
      }
      return <>
        <Details fields={[
          [l("付款结果", "Payment result"), <PaymentStatusBadge payment={{ status: funds.payment_status, checkout_needs_review: funds.checkout_needs_review }} />],
          [l("资金可用", "Funds availability"), availability],
          [t("Funds account"), funds.account_id ?? "—"],
          [l("本笔净入账", "Net credit for this order"), credited === null ? "—" : money(credited)],
          [l("账户当前可代付", "Account currently available for payouts"), funds.account_available_minor === null ? "—" : money(funds.account_available_minor)],
          [l("上游账单", "Upstream statement"), funds.open_difference ? l("待核对差异", "Open discrepancy") : funds.statement_matched ? l("已匹配", "Matched") : l("暂无匹配记录", "No match recorded")],
          [l("上游结算凭证", "Upstream settlement evidence"), funds.receipt_recorded ? l("平台已登记", "Recorded by the platform") : l("暂无登记", "No receipt recorded")],
          [l("结算进度", "Settlement progress"), settlement],
          [l("结算到期时间", "Settlement maturity"), funds.due_at ? dateTime(funds.due_at) : "—"],
          ...(settled === null ? [] : [[l("本笔累计已分配出款", "Recorded transfer allocation"), money(settled)] as [string, string]]),
        ]} />
        {funds.settlements.length > 0 && <ul className="merchant-payment-batches">
          {funds.settlements.map(batch => <li key={batch.id}>
            <strong>{money(batch.allocated_minor)}</strong> · {batch.state === "confirmed" ? l("已登记出款", "Transfer recorded") : l("结算锁定中", "Reserved for settlement")}
            <div className="wrap">{batch.id}</div>
            {batch.transfer_reference && <div className="wrap">{l("登记出款流水", "Recorded transfer reference")} · {batch.transfer_reference} {batch.paid_on ? " · " + date(batch.paid_on) : ""}</div>}
          </li>)}
        </ul>}
      </>;
    }}</LoadState>
  </section>;
}
