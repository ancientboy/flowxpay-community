import { useEffect, useState } from "react";
import { PlatformOrderSummary } from "./PlatformOrderSummary";
import { collectionStatuses, PaymentStatusBadge } from "./CollectionEvents";
import { useLocale } from "./locale";
import { platformAPI } from "./platformEnvironment";
import { LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

type CollectionOrder = {
  id: string; merchant: string; reference: string; currency: string; amount_minor: string;
  status: string; created_at: string; updated_at: string; channel_id: string; channel_name: string;
  market: string; method: string; checkout_needs_review: boolean; notice_state: string; live_test: boolean;
  credited_minor: string | null; credited_at: string | null; has_confirmed_allocation: boolean;
};
type CollectionPage = {
  rows: CollectionOrder[]; next: { created_at: string; id: string } | null;
  channels: { id: string; name: string }[];
};

export function PlatformCollectionOrders({ merchant, initialReference, token, revision, openOrder, openEvidence }: {
  merchant: string; initialReference?: string; token: string; revision: number;
  openOrder: (row: CollectionOrder) => void; openEvidence: (id: string) => void;
}) {
  const { locale, t, exactMoney, dateTime } = useLocale();
  const l = (zh: string, en: string) => locale === "zh-CN" ? zh : en;
  const empty = { reference: "", channel: "", currency: "", status: "", notice: "", from: "", to: "", live_test: "" };
  const [draft, setDraft] = useState({ ...empty, reference: initialReference ?? "" });
  const [filters, setFilters] = useState(draft);
  const [cursors, setCursors] = useState([""]);
  const params = new URLSearchParams({ merchant, ...filters, after: cursors.at(-1) ?? "" });
  const state = useWorkspaceData<CollectionPage>(platformAPI + "/collection-orders?" + params, token);
  const [channels, setChannels] = useState<CollectionPage["channels"]>([]);
  useEffect(() => { if (state.data) setChannels(state.data.channels); }, [state.data]);
  useEffect(() => { if (revision > 0) state.refresh(); }, [revision]);
  const field = (key: keyof typeof draft, value: string) => setDraft(previous => ({ ...previous, [key]: value }));
  const noticeLabels: Record<string, string> = {
    none: l("暂无事件", "No event"), unconfigured: l("未配置", "Unconfigured"),
    pending: l("待通知", "Pending"), delivered: l("已送达", "Delivered"), dead: l("投递失败", "Delivery failed"),
  };
  const paymentStates = [
    ["queued", l("排队中", "Queued")], ["routing", l("派发中", "Routing")],
    ["requires_action", l("等待付款", "Awaiting payment")], ["review", l("待核对", "Needs review")],
    ["pending_confirmation", l("待确认", "Pending confirmation")], ["succeeded", l("成功", "Succeeded")],
    ["failed", l("失败", "Failed")], ["cancelled", l("已取消", "Cancelled")], ["expired", l("已过期", "Expired")],
  ];
  const title = t("Payment list");
  const columns = [t("Platform reference"), t("Channel"), t("Amount"), t("Status"), l("净入账", "Net credited"),
    l("结算 / 通知", "Settlement / notification"), t("Created"), l("查证", "Evidence")];

  const rows = state.data?.rows;
  const tabs = ["", "succeeded", "requires_action", "review", "failed"];
  const statusLabel = (status: string) => t(collectionStatuses[status] ?? status);

  return <section className="payout-book collection-report">
    <p className="payout-scope">{l("按币种汇总当前页的代收订单金额。净入账以订单记录为准。", "Amounts are grouped by currency for this page. Net credits are shown per order.")}</p>
    {rows && <PlatformOrderSummary rows={rows} scope={rows.map(row => row.checkout_needs_review ? {...row, status: "review"} : row)}
      label={t("Collection order amount")} statusLabel={statusLabel} isoScale />}
    <div className="payout-ledger collection-ledger">
    <div className="payout-ledger-head"><h2>{title}</h2>{rows && <span>{l("本页", "This page")} · {rows.length}</span>}</div>
    <div className="payout-tabs" role="group" aria-label={t("Status")}>
      {tabs.map(status => <button type="button" key={status} className="payout-tab" aria-pressed={filters.status === status}
        onClick={() => {
          if (filters.status === status) return;
          const next = { ...filters, status };
          setDraft(next); setFilters(next); setCursors([""]); state.reload();
        }}>{status ? statusLabel(status) : t("All collections")}</button>)}
    </div>
    <form className="platform-finance-filters period-filters" onSubmit={event => {
      event.preventDefault();
      state.reload();
      setCursors([""]);
      setFilters({ ...draft, reference: draft.reference.trim() });
    }}>
      <label>{l("订单号", "Order reference")}<input id="ops-payment-reference" value={draft.reference}
        placeholder={l("平台、商户或上游单号", "Platform, merchant or upstream")}
        onChange={event => field("reference", event.target.value)} maxLength={80} /></label>
      <label>{t("Channel")}<select value={draft.channel} onChange={event => field("channel", event.target.value)}>
        <option value="">{l("全部通道", "All channels")}</option>
        {channels.map(channel => <option key={channel.id} value={channel.id}>{channel.name} · {channel.id}</option>)}
      </select></label>
      <label>{t("Currency")}<input value={draft.currency} maxLength={3} pattern="[A-Z]{3}"
        onChange={event => field("currency", event.target.value.toUpperCase())} /></label>
      <label>{t("Status")}<select value={draft.status} onChange={event => field("status", event.target.value)}>
        <option value="">{l("全部状态", "All statuses")}</option>
        {paymentStates.map(([key, label]) => <option key={key} value={key}>{label}</option>)}
      </select></label>
      <label>{l("商户通知", "Merchant notification")}<select value={draft.notice} onChange={event => field("notice", event.target.value)}>
        <option value="">{l("全部状态", "All statuses")}</option>
        {Object.entries(noticeLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}
      </select></label>
      <label>{t("Start date")} (UTC)<input type="date" value={draft.from} required={Boolean(draft.to)}
        onChange={event => field("from", event.target.value)} /></label>
      <label>{t("End date")} (UTC)<input type="date" value={draft.to} min={draft.from} required={Boolean(draft.from)}
        onChange={event => field("to", event.target.value)} /></label>
      <label className="platform-checkbox"><input type="checkbox" checked={draft.live_test === "1"}
        onChange={event => field("live_test", event.target.checked ? "1" : "")} />
        {l("仅实付测试单", "Live test orders only")}</label>
      <button className="secondary">{t("Search")}</button>
      <button className="secondary" type="button" onClick={() => {
        setDraft(empty);
        setFilters(empty);
        state.reload();
        setCursors([""]);
      }}>{t("Clear")}</button>
    </form>
    <div className="workspace-panel period-report collection-results"><LoadState state={state} label={title}>{page => <>
      {page.rows.length ? <WorkspaceTable label={title} columns={columns}>
        {page.rows.map(row => <tr key={row.id}>
          <th scope="row" data-label={columns[0]} className="order-number">
            <button className="text-button wrap" onClick={() => openOrder(row)}>{row.id}</button>
            {row.live_test && <div className="muted small">{l("实付测试", "Live payment test")}</div>}
            <div className="muted small">{row.merchant} · {row.reference}</div>
          </th>
          <td data-label={columns[1]}>{row.channel_name || row.channel_id}</td>
          <td data-label={columns[2]}>{exactMoney(row.amount_minor, row.currency, true)}</td>
          <td data-label={columns[3]}><PaymentStatusBadge payment={row} /></td>
          <td data-label={columns[4]}>{row.credited_minor === null ? "—" : exactMoney(row.credited_minor, row.currency, true)}
            {row.credited_at && <div className="muted small">{dateTime(row.credited_at)}</div>}</td>
          <td data-label={columns[5]}>{row.has_confirmed_allocation ? l("有已确认分摊", "Confirmed allocation") : "—"}
            <div>{noticeLabels[row.notice_state] ?? row.notice_state}</div></td>
          <td data-label={columns[6]}>{dateTime(row.created_at)}</td>
          <td data-label={columns[7]}><button className="text-button" type="button" onClick={() => openEvidence(row.id)}>{l("查证", "View evidence")}</button></td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("No payments match your search.")}</p>}
      {(cursors.length > 1 || page.next) && <div className="header-actions period-presets">
        <button className="secondary" type="button" disabled={cursors.length === 1} onClick={() => { state.reload(); setCursors(previous => previous.slice(0, -1)); }}>{l("上一页", "Previous")}</button>
        <button className="secondary" type="button" disabled={!page.next} onClick={() => {
          if (page.next) {
            state.reload();
            setCursors([...cursors, page.next.created_at + "|" + page.next.id]);
          }
        }}>{l("下一页", "Next")}</button>
      </div>}
    </>}</LoadState></div>
    </div>
  </section>;
}
