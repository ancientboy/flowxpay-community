import { ControlAction } from "./WorkspaceControls";
import { OrderFollowUp, type FollowUp } from "./OrderFollowUp";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";
import { Details, LoadState, WorkspaceTable, useWorkspaceData } from "./WorkspaceInventoryUI";
import { ApiError, request } from "./api";
import { useRef, useState } from "react";
import { platformAPI } from "./platformEnvironment";

type Dispatch = {
  reference: string; final_upstream_reference: string | null; attempted_at: string | null;
  submitted_at: string | null; final_at: string | null; lookup_attempts: number;
  lookup_result: string | null; lookup_updated_at: string | null; next_lookup_at: string | null;
};
type OrderEvidenceData = {
  order_id: string; merchant_id: string; merchant_reference: string; direction: "collection" | "payout";
  currency: string; channel_id: string; amount_minor: string; status: string; updated_at: string;
  account_id: string | null; settlement_due_at: string | null; payout_held_minor: string | null;
  checkout_needs_review: boolean; can_record_checkout_unavailable: boolean;
  can_cancel_unaccepted_payout?: boolean;
  manual_cancellation?: {reference: string; evidence: string; operator: string; cancelled_at: string} | null;
  dispatch: Dispatch | null; open_differences: number; follow_up: FollowUp | null;
  ledger: {id: string; kind: string; created_at: string; available_delta: string; payout_held_delta: string; settlement_held_delta: string; dispute_held_delta: string}[];
  notifications: {id: string; status: string; state: string; attempts: number; created_at: string; last_attempt_at: string | null}[];
  statements: {id: string; batch_id: string; kind: string; reference: string; date: string; state: string; gross_minor: string; fee_minor: string; net_minor: string}[];
  differences: {import_id: string; batch_id: string; line: number; issue: string; resolved_at: string | null; resolved_by: string | null; reason: string | null}[];
  settlements: {id: string; state: string; statement_batch_id: string; statement_entry_id: string; allocated_minor: string; transfer_id: string | null; transfer_reference: string | null; paid_on: string | null}[];
};

export function OrderEvidence({ id, token, close, operator }: { id: string; token: string; close: () => void; operator: boolean }) {
  const { locale, dateTime, exactMoney } = useLocale();
  const l = (cn: string, en: string) => locale === "zh-CN" ? cn : en;
  const title = l("订单查证", "Order evidence");
  const dispatchTitle = l("原通道派发与查证", "Original dispatch and lookup");
  const ledgerTitle = l("资金流水", "Fund ledger");
  const ledgerHeading = useRef<HTMLHeadingElement>(null);
  const noticeHeading = useRef<HTMLHeadingElement>(null);
  const noticeTitle = l("商户通知", "Merchant notifications");
  const statementTitle = l("上游账单", "Upstream statements");
  const differenceTitle = l("账单差异", "Statement discrepancies");
  const settlementTitle = l("结算批次与登记出款凭证", "Settlement batches and recorded transfer evidence");
  const state = useWorkspaceData<OrderEvidenceData>(platformAPI + "/order-evidence?" + new URLSearchParams({ order_id: id }), token);
  const [confirmingCancellation, setConfirmingCancellation] = useState(false);
  const [confirmingCheckout, setConfirmingCheckout] = useState(false);
  const [savingCheckout, setSavingCheckout] = useState(false);
  const [checkoutError, setCheckoutError] = useState("");
  async function recordCheckout() {
    setSavingCheckout(true);
    setCheckoutError("");
    try {
      await request<void>(platformAPI + "/order-evidence", token, { method: "POST",
        body: JSON.stringify({ order_id: id, action: "checkout_unavailable" }) });
      setConfirmingCheckout(false);
      state.reload();
    } catch {
      setCheckoutError(l("登记结果未确认，请刷新订单证据。", "Refresh the order evidence to verify the result."));
    } finally {
      setSavingCheckout(false);
    }
  }
  const date = (value: string | null) => value ? dateTime(value) : "—";

  return <Dialog title={title} drawer close={close}>
    {state.error instanceof ApiError && state.error.status === 404
      ? <p role="status">{l("未查到该平台订单。", "No order was found for that platform ID.")}</p>
      : <LoadState state={state} label={title}>{order => {
        const dispatch = order.dispatch;
        const money = (value: string | null) => value === null ? "—" : exactMoney(value, order.currency, true);
        const instructions = [];
        if (order.checkout_needs_review) instructions.push(
          l("付款码已失效。继续通过原通道查单，并核对上游账单。", "The payment code has expired. Check the original order and upstream statement."));
        if (order.status === "pending_confirmation") instructions.push(
          l("沿原通道查证。结果明确前保留占用，避免重复提交。", "Verify on the original channel. Keep the hold and avoid resubmitting until the outcome is confirmed."));
        if (order.open_differences > 0) instructions.push(
          l("财务核对原账单差异和截止范围，确认凭证后记录处理。", "Finance reviews the original statement discrepancy and coverage, then records the evidence."));
        if (order.notifications.some(event => event.state === "dead")) instructions.push(
          l("运营核对商户通知地址与最近投递失败原因，再按原事件处理。", "Operations checks the merchant endpoint and the last delivery failure before handling the original event."));
        if (order.notifications.some(event => event.state === "unconfigured")) instructions.push(
          l("商户管理员配置通知地址后，运营可在通知记录中处理原事件。", "The merchant admin configures the notification endpoint. Operations can then handle the original event from its delivery record."));
        return <>
          <OrderFollowUp orderId={order.order_id} owner={order.follow_up} operator={operator} instructions={instructions} token={token} refresh={state.reload} />
          {operator && order.can_record_checkout_unavailable && <div className="panel-note">
            {confirmingCheckout ? <>
              <p>{l("确认上游付款码已失效后，收起商户端付款入口并继续核对原单。", "After confirming the code has expired, hide the merchant checkout link and continue checking the original order.")}</p>
              <div className="header-actions">
                <button className="primary" type="button" disabled={savingCheckout || Boolean(checkoutError)} onClick={() => void recordCheckout()}>{l("确认登记付款码失效", "Confirm payment code expiration")}</button>
                <button className="secondary" type="button" disabled={savingCheckout} onClick={() => { setConfirmingCheckout(false); setCheckoutError(""); }}>{l("取消", "Cancel")}</button>
              </div>
              {checkoutError && <p role="alert">{checkoutError}</p>}
            </> : <button className="secondary" type="button" onClick={() => setConfirmingCheckout(true)}>{l("登记付款码失效", "Record expired payment code")}</button>}
          </div>}
          {operator && order.can_cancel_unaccepted_payout && dispatch && <div className="panel-note">
            <button className="secondary" type="button" onClick={() => setConfirmingCancellation(true)}>
              {l("确认未受理并取消", "Cancel unaccepted payout")}
            </button>
            {confirmingCancellation && <ControlAction
              title={l("确认未受理并取消代付", "Cancel an unaccepted payout")}
              submitLabel={l("取消并释放冻结", "Cancel and release hold")}
              close={() => setConfirmingCancellation(false)} refresh={state.reload}
              action={async form => {
                await request<void>(platformAPI + "/order-evidence", token, {method: "POST", body: JSON.stringify({
                  order_id: order.order_id, action: "cancel_unaccepted_payout", dispatch_reference: dispatch.reference,
                  evidence: String(form.get("evidence") ?? "").trim(),
                })});
              }}>
              <p>{l("仅在上游确认该参考号没有已受理或待处理的付款指令时操作。确认后向商户释放原单冻结。",
                "Use only after the provider confirms no accepted or pending payout for this reference. Confirmation releases the original hold.")}</p>
              <Details fields={[[l("原派发号", "Original dispatch reference"), dispatch.reference],
                [l("释放金额", "Amount to release"), money(order.payout_held_minor)]]} />
              <label>{l("上游确认依据（工单号或核对记录）", "Provider confirmation (case number or verification record)")}
                <textarea name="evidence" required maxLength={500} rows={3} />
              </label>
            </ControlAction>}
          </div>}
          {order.manual_cancellation && <Details fields={[
            [l("人工取消依据", "Manual cancellation evidence"), order.manual_cancellation.evidence],
            [l("操作人 / 时间", "Operator / time"), order.manual_cancellation.operator + " · " + date(order.manual_cancellation.cancelled_at)],
          ]} />}
          <button className="secondary" type="button" onClick={() => { setCheckoutError(""); state.reload(); }}>{l("刷新证据", "Refresh evidence")}</button>
          <div className="header-actions">
            <button className="secondary" type="button" onClick={() => ledgerHeading.current?.focus()}>{ledgerTitle}</button>
            <button className="secondary" type="button" onClick={() => noticeHeading.current?.focus()}>{noticeTitle}</button>
          </div>
          <Details fields={[
            [l("平台单号", "Platform order ID"), order.order_id],
            [l("商户 / 商户单号", "Merchant / merchant reference"), order.merchant_id + " · " + order.merchant_reference],
            [l("方向 / 原通道", "Direction / original channel"), (order.direction === "payout" ? l("代付", "Payout") : l("代收", "Collection")) + " · " + order.channel_id],
            [l("订单金额", "Order amount"), money(order.amount_minor)],
            [l("平台状态 / 更新时间", "Platform status / updated"), order.status + " · " + date(order.updated_at)],
            [l("原资金账户", "Original fund account"), order.account_id ?? "—"],
            ...(order.direction === "payout"
              ? [[l("原单冻结余额", "Remaining hold for this order"), money(order.payout_held_minor)] as [string, string]]
              : [[l("结算到期时间", "Settlement due"), date(order.settlement_due_at)] as [string, string]]),
          ]} />
          <h3>{dispatchTitle}</h3>
          {dispatch ? <Details fields={[
            [l("原派发号", "Original dispatch reference"), dispatch.reference],
            [l("已核验上游单号", "Verified upstream reference"), dispatch.final_upstream_reference ?? "—"],
            [l("首尝试 / 已提交 / 终态", "Attempted / submitted / finalized"), [dispatch.attempted_at, dispatch.submitted_at, dispatch.final_at].map(date).join(" · ")],
            [l("最近查证结果 / 时间", "Last lookup result / time"), (dispatch.lookup_result || "—") + " · " + date(dispatch.lookup_updated_at)],
            [l("查证次数 / 下次时间", "Lookup attempts / next run"), dispatch.lookup_attempts + " · " + date(dispatch.next_lookup_at)],
          ]} /> : <p>{l("暂无派发记录。", "No dispatch record is available.")}</p>}
          <p className="muted small">{l("各明细最多显示 20 条，待处理差异优先。", "Each list shows up to 20 records, with open discrepancies first.")}</p>
          <h3 ref={ledgerHeading} tabIndex={-1}>{ledgerTitle}</h3>
          {order.ledger.length ? <WorkspaceTable label={ledgerTitle} columns={[l("流水 / 类型", "Entry / type"),
            l("可用变动", "Available change"), l("代付冻结变动", "Payout hold change"),
            l("结算冻结变动", "Settlement hold change"), l("争议冻结变动", "Dispute hold change"), l("时间", "Time")]}>
            {order.ledger.map(row => <tr key={row.id}><td>{row.id} · {row.kind}</td><td>{money(row.available_delta)}</td>
              <td>{money(row.payout_held_delta)}</td><td>{money(row.settlement_held_delta)}</td>
              <td>{money(row.dispute_held_delta)}</td><td>{date(row.created_at)}</td></tr>)}
          </WorkspaceTable> : <p>{l("暂无关联的资金流水。", "No linked ledger entry is available.")}</p>}
          <h3 ref={noticeHeading} tabIndex={-1}>{noticeTitle}</h3>
          {order.notifications.length ? <WorkspaceTable label={noticeTitle} columns={[l("事件", "Event"), l("投递状态", "Delivery state"), l("尝试 / 最近时间", "Attempts / last tried")]}>
            {order.notifications.map(row => <tr key={row.id}><td>{row.status}<div className="muted small">{row.id}</div></td><td>{row.state}</td><td>{row.attempts} · {date(row.last_attempt_at)}</td></tr>)}
          </WorkspaceTable> : <p>{l("暂无商户通知事件。", "No merchant notification event is available.")}</p>}
          <h3>{statementTitle}</h3>
          {order.statements.length ? <WorkspaceTable label={statementTitle} columns={[l("批次 / 明细", "Batch / entry"), l("日期 / 状态", "Date / state"), l("本金 / 费用 / 净额", "Gross / fee / net")]}>
            {order.statements.map(row => <tr key={row.id}><td>{row.batch_id} / {row.id}<div>{row.kind} · {row.reference}</div></td><td>{row.date} · {row.state}</td><td>{[row.gross_minor, row.fee_minor, row.net_minor].map(money).join(" / ")}</td></tr>)}
          </WorkspaceTable> : <p>{l("暂无关联的上游账单明细。", "No linked upstream statement entry is available.")}</p>}
          <h3>{differenceTitle} · {order.open_differences} {l("待处理", "open")}</h3>
          {order.differences.length ? <WorkspaceTable label={differenceTitle} columns={[l("批次 / 行", "Batch / line"), l("差异", "Discrepancy"), l("处理记录", "Resolution")]}>
            {order.differences.map(row => <tr key={row.import_id + "/" + row.line}><td>{row.batch_id} / {row.line}</td><td>{row.issue}</td><td>{row.resolved_at ? <>{date(row.resolved_at)} · {row.resolved_by}<div>{row.reason}</div></> : l("待处理", "Open")}</td></tr>)}
          </WorkspaceTable> : <p>{l("暂无关联的账单差异。", "No linked statement discrepancy is available.")}</p>}
          <h3>{settlementTitle}</h3>
          {order.settlements.length ? <WorkspaceTable label={settlementTitle} columns={[l("结算批次 / 状态", "Settlement batch / state"), l("分配金额 / 来源", "Allocation / source"), l("登记出款流水 / 日期", "Recorded transfer reference / date")]}>
            {order.settlements.map(row => <tr key={row.id}><td>{row.id}<div>{row.state}</div></td><td>{money(row.allocated_minor)}<div>{row.statement_batch_id} / {row.statement_entry_id}</div></td><td>{row.transfer_reference ?? "—"} / {row.paid_on ?? "—"}</td></tr>)}
          </WorkspaceTable> : <p>{l("暂无关联的结算分配。", "No linked settlement allocation is available.")}</p>}
        </>;
      }}</LoadState>}
  </Dialog>;
}
