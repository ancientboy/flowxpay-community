import { elapsedTime, HandlingNote, OrderTime, useOrderClock } from "./OrderContext";
import { PlatformOrderSummary, matchesOrderStatus } from "./PlatformOrderSummary";
import { useLocale } from "./locale";
import { useRef, useState, type ReactNode } from "react";
import { ApiError, request, type OrderFee } from "./api";
import { Dialog } from "./Dialog";
import { base, message, type MemberSession } from "./WorkspaceAccess";
import { Details, InventoryForm, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

import { channelTitle } from "./ChannelIdentity";
import { merchantQuotesPath, OrderQuote, rateText, type MerchantQuote } from "./ChannelPricing";

const types = { bank_account: "Bank account", pix: "Pix", upi: "UPI" };
type RecipientType = keyof typeof types;

export const payoutStatuses: Record<string, string> = {
  draft: "Awaiting review", processing: "Processing", pending_confirmation: "Pending confirmation",
  succeeded: "Succeeded", failed: "Failed", rejected: "Rejected", queued: "Queued", pending_approval: "Pending approval",
  routing: "Processing", requires_action: "Requires action", cancelled: "Cancelled", expired: "Expired",
};
const reasons: Record<string, string> = {
  review_rejected: "Rejected by reviewer", channel_rejected: "Declined by the mock channel",
  payout_account_disabled: "Payout account disabled before submission",
  payout_rate_limited: "Payout account submission rate exceeded",
  payout_daily_limit_exceeded: "Payout account daily limit exceeded",
};

interface Recipient { id: string; type: RecipientType; masked_destination: string }
interface PayoutAccount {
  account_id: string; currency: string; recipient_type: RecipientType; enabled: boolean;
  available_minor: number; reserved_minor: number; max_payout_minor: number;
}
interface Payout {
  id: string; account_id: string; recipient_id: string; recipient_type: RecipientType; currency: string;
  quote?: OrderFee | null; platform_channel_id?: string;
  amount_minor: number; merchant_reference: string; status: string; created_by: string;
  reviewed_by?: string; reviewed_at?: string; channel_reference?: string; transfer_reference?: string; failure_code?: string;
}

type PayoutOrder = { created_at?: string; reviewed_by?: string; reviewed_at?: string } & Pick<Payout, "id" | "merchant_reference" | "amount_minor" | "currency" | "quote" | "status">;

export function Payouts({ token, member, workflow, initialOrder = "", pendingOnly = false }: { token: string; member: MemberSession; workflow: boolean; initialOrder?: string; pendingOnly?: boolean }) {
  return workflow ? <SandboxPayoutWorkflow token={token} member={member} pendingOnly={pendingOnly} /> : <RecordedPayouts token={token} initialOrder={initialOrder} pendingOnly={pendingOnly} />;
}

function PayoutOrders({ state, sandbox, refresh, find, select, actions, initialStatus = "" }: {
  state: ReturnType<typeof useWorkspaceData<PayoutOrder[]>>;
  initialStatus?: string; sandbox?: boolean;
  refresh: () => void;
  find: (reference: string) => void;
  select: (id: string) => void;
  actions?: ReactNode;
}) {
  const { t, money } = useLocale();
  const now = useOrderClock();
  const [status, setStatus] = useState(initialStatus);
  return <>
    {state.data && !state.error && <PlatformOrderSummary rows={state.data} payout selected={status} onSelect={setStatus} isoScale={false} />}
    <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t(sandbox ? "Sandbox payout orders" : "Payout orders")}</h2>
      <div className="header-actions payout-actions">
        <button className="secondary" onClick={refresh}>{t("Refresh payouts")}</button>
        {actions}
      </div>
    </div>
    <form className="payout-search" onSubmit={event => {
      event.preventDefault();
      setStatus("");
      find(String(new FormData(event.currentTarget).get("reference") ?? "").trim());
    }}>
      <label htmlFor="payout-find">{t("Order reference or ID")}</label>
      <input id="payout-find" name="reference" maxLength={80} pattern="[a-zA-Z0-9._:\-]{1,80}" />
      <button className="secondary">{t("Find payouts")}</button>
    </form>
    <LoadState state={state} label={t("payout orders")}>{orders => {
      const items = orders.filter(row => matchesOrderStatus(row, status, true));
      return items.length ?
      <WorkspaceTable label={t("Payout orders")} columns={[t("Merchant reference"), t("Payout ID"), t("Amount"), t("Status"), t("Created")]}>
        {items.map(item => <tr key={item.id}>
          <td><button className="text-button wrap" onClick={() => select(item.id)}>{item.merchant_reference}</button></td>
          <td className="resource-id order-number">{item.id}</td>
          <td>{money(item.amount_minor, item.currency)}</td><td>{t(payoutStatuses[item.status] ?? item.status)}<HandlingNote record={item} /></td>
          <td><OrderTime at={item.created_at} now={now} /></td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("No payouts found.")}</p>;}}</LoadState>
    </section>
  </>;
}

function PayoutFacts({ item, children }: { item: PayoutOrder; children: [string, ReactNode][] }) {
  const { t, money } = useLocale();
  return <Details fields={[
    [t("Payout ID"), item.id], [t("Merchant reference"), item.merchant_reference],
    [t("Status"), t(payoutStatuses[item.status] ?? item.status)], [t("Amount"), money(item.amount_minor, item.currency)],
    [t("Quoted merchant fee"), <OrderQuote quote={item.quote} currency={item.currency} />],
    ...children,
  ]} />;
}

type PayoutRecord = PayoutOrder & {
  product_id: string; fund_account_id?: string; recipient_id: string; market: string; method: string;
  created_at: string; updated_at: string;
  attempted_at?: string | null; submitted_at?: string | null; final_at?: string | null;
  held_minor?: string | null; scale?: number | null; lookup_updated_at?: string | null; next_lookup_at?: string | null;
};

function RecordedPayouts({ token, initialOrder, pendingOnly }: { token: string; initialOrder: string; pendingOnly: boolean }) {
  const { t, dateTime, exactMoney, locale } = useLocale();
  const [search, setSearch] = useState(initialOrder);
  const [selected, setSelected] = useState(initialOrder);
  const [pending, setPending] = useState(pendingOnly);
  const path = base + "/payouts";
  const balances = useWorkspaceData<{id: string; currency: string; scale: number | null; available_minor: string; held_minor: string}[]>(base + "/payout-balances", token);
  const params = new URLSearchParams(search ? { reference: search } : {});
  if (pending) params.set("pending", "1");
  const payouts = useWorkspaceData<PayoutOrder[]>(path + "?" + params, token);
  const detail = useWorkspaceData<PayoutRecord>(selected ? path + "/" + encodeURIComponent(selected) : undefined, token);
  return <>
    <PayoutOrders state={payouts} actions={pending && <button type="button" className="secondary" onClick={() => { setPending(false); payouts.reload(); }}>
      {locale === "zh-CN" ? "待确认筛选 · 查看全部" : "Pending filter · Show all"}
    </button>} refresh={() => { payouts.reload(); balances.reload(); }} select={id => { detail.reload(); setSelected(id); }}
      find={reference => { setSearch(reference); payouts.reload(); }} />
    <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Payout account balances")}</h2></div>
      <LoadState state={balances} label={t("payout accounts")}>{items => items.length ?
        <WorkspaceTable label={t("Payout account balances")} columns={[t("Funds account"), t("Currency"), t("Available"), t("Reserved")]}>
          {items.map(item => <tr key={item.currency + "/" + item.id}><td>{item.id}</td><td>{item.currency}</td>
            <td>{item.scale === null ? item.available_minor + " " + t("minor units") : exactMoney(item.available_minor, item.currency, item.scale)}</td>
            <td>{item.scale === null ? item.held_minor + " " + t("minor units") : exactMoney(item.held_minor, item.currency, item.scale)}</td></tr>)}
        </WorkspaceTable> : <p className="muted empty">{t("No accounts are configured for payouts.")}</p>}</LoadState>
    </section>
    {selected && <Dialog drawer title={t("Payout details")} close={() => setSelected("")}>
      <LoadState state={detail} label={t("payout details")}>{item => <>
        {item.status === "pending_confirmation" && <p className="panel-note" role="status">{locale === "zh-CN"
          ? "平台正在核查。请刷新这笔订单的状态，避免重复发起代付。"
          : "FlowXPay is verifying this payout. Refresh this order before taking further action to avoid a duplicate payout."}</p>}
        <PayoutFacts item={item}>{[
          [locale === "zh-CN" ? "处理记录" : "Handling record", <HandlingNote record={{attempted_at: item.attempted_at, lookup_at: item.lookup_updated_at}} />],
          ...(elapsedTime(item.created_at, item.final_at, locale) ? [[locale === "zh-CN" ? "创建至最终确认" : "Creation to final confirmation", elapsedTime(item.created_at, item.final_at, locale)] as [string, ReactNode]] : []),
          [t("Product ID"), item.product_id],
          [t("Funds account"), item.fund_account_id ?? "—"],
          [locale === "zh-CN" ? "本笔剩余冻结" : "Remaining hold for this payout",
            item.held_minor == null ? "—" : item.scale == null
              ? item.held_minor + " " + item.currency + " · " + t("minor units")
              : exactMoney(item.held_minor, item.currency, item.scale)],
          ...(item.lookup_updated_at ? [[locale === "zh-CN" ? "最近上游查证" : "Last upstream lookup", dateTime(item.lookup_updated_at)] as [string, string]] : []),
          ...(item.status === "pending_confirmation" && item.next_lookup_at
            ? [[locale === "zh-CN" ? "下次自动查证" : "Next automatic lookup", dateTime(item.next_lookup_at)] as [string, string]] : []),
          [t("Method"), item.market + " · " + t(item.method)], [t("Recipient ID"), item.recipient_id],
          [t("Created"), dateTime(item.created_at)], [t("Updated"), dateTime(item.updated_at)],
          ...(item.attempted_at ? [[t("Dispatch attempted at"), dateTime(item.attempted_at)] as [string, string]] : []),
          ...(item.submitted_at ? [[t("Submitted to channel at"), dateTime(item.submitted_at)] as [string, string]] : []),
          ...(item.final_at ? [[locale === "zh-CN" ? "确认最终结果" : "Final result confirmed", dateTime(item.final_at)] as [string, string]] : []),
        ]}</PayoutFacts>
        <div className="detail-actions"><button className="secondary" onClick={() => { detail.reload(); payouts.reload(); balances.reload(); }}>{t("Refresh status")}</button></div>
      </>}</LoadState>
    </Dialog>}
  </>;
}

function SandboxPayoutWorkflow({ token, member, pendingOnly }: { token: string; member: MemberSession; pendingOnly: boolean }) {
  const locale = useLocale();
  const { t, money } = locale;
  const [search, setSearch] = useState("");
  const [form, setForm] = useState<"draft" | "recipient">();
  const [selected, setSelected] = useState("");
  const [accountID, setAccountID] = useState("");
  const quotes = useWorkspaceData<MerchantQuote[]>(merchantQuotesPath, token);
  const accounts = useWorkspaceData<PayoutAccount[]>("/v1/payout-accounts", token);
  const recipients = useWorkspaceData<Recipient[]>("/v1/payout-recipients", token);
  const payouts = useWorkspaceData<Payout[]>("/v1/payouts" + (search ? "?reference=" + encodeURIComponent(search) : ""), token);
  const payoutQuote = quotes.data?.find(item => item.direction === "payout" && item.account_id === accountID);
  const account = accounts.data?.find(item => item.account_id === accountID && item.enabled);
  const eligible = recipients.data?.filter(recipient => recipient.type === account?.recipient_type) ?? [];
  const canDraft = import.meta.env.MODE !== "community-demo" && member.role !== "viewer";

  function refresh() {
    payouts.reload();
    accounts.reload();
    quotes.reload();
    recipients.reload();
  }

  return <>
    <PayoutOrders sandbox initialStatus={pendingOnly ? "pending_confirmation" : ""} state={payouts} refresh={refresh} select={setSelected}
      find={reference => { setSearch(reference); payouts.reload(); }}
      actions={canDraft && <button className="primary" onClick={() => { setAccountID(""); setForm("draft"); }}>{t("New payout draft")}</button>} />
    <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Payout account balances")}</h2></div>
      <LoadState state={accounts} label={t("payout accounts")}>{items => items.length ?
        <WorkspaceTable label={t("Payout account balances")} columns={[t("Account"), t("Available"), t("Reserved"), t("Per-payout limit")]}>
          {items.map(item => <tr key={item.account_id}><td><span className="resource-id">{item.account_id}</span>
            {!item.enabled && <span className="tag gray">{t("Disabled")}</span>}</td>
            <td>{money(item.available_minor, item.currency)}</td><td>{money(item.reserved_minor, item.currency)}</td>
            <td>{money(item.max_payout_minor, item.currency)}</td>
          </tr>)}
        </WorkspaceTable> : <p className="muted empty">{t("No accounts are configured for payouts.")}</p>}</LoadState>
    </section>
    <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Sandbox recipients")}</h2>
        {canDraft && <button className="secondary" onClick={() => setForm("recipient")}>{t("Add sandbox recipient")}</button>}
      </div>
      <LoadState state={recipients} label={t("recipients")}>{items => items.length ?
        <WorkspaceTable label={t("Sandbox recipients")} columns={[t("Recipient ID"), t("Type"), t("Destination")]}>
          {items.map(item => <tr key={item.id}><td className="resource-id">{item.id}</td>
            <td>{t(types[item.type])}</td><td>{item.masked_destination}</td></tr>)}
        </WorkspaceTable> : <p className="muted empty">{t("No sandbox recipients yet.")}</p>}</LoadState>
    </section>
    {form === "recipient" && <InventoryForm title={t("Add sandbox recipient")} path="/v1/payout-recipients" token={token}
      close={() => setForm(undefined)} created={() => { setForm(undefined); recipients.reload(); }}>
      <label htmlFor="payout-recipient-type">{t("Recipient type")}</label>
      <select id="payout-recipient-type" name="type">
        {Object.entries(types).map(([value, label]) => <option value={value} key={value}>{t(label)}</option>)}
      </select>
      <label htmlFor="test-suffix">{t("Fictional destination suffix")}</label>
      <input id="test-suffix" name="test_suffix" inputMode="numeric" autoComplete="off" minLength={4} maxLength={4} pattern="[0-9]{4}" required />
    </InventoryForm>}
    {form === "draft" && <InventoryForm title={t("New payout draft")} path="/v1/payouts" token={token} submitLabel={t("Save draft")}
      close={() => setForm(undefined)} created={() => { setForm(undefined); setSearch(""); payouts.reload(); }}
      unavailable={!account || !eligible.length} payload={data => ({
        ...Object.fromEntries(data), currency: account?.currency, amount_minor: Number(data.get("amount_minor")),
      })}>
      <label htmlFor="payout-account">{t("Payout account")}</label>
      <select id="payout-account" name="account_id" value={accountID} onChange={event => setAccountID(event.target.value)} required>
        <option value="" disabled>{t("Select an account")}</option>
        {accounts.data?.filter(item => item.enabled).map(item => {
          const channel = quotes.data?.find(quote => quote.direction === "payout" && quote.account_id === item.account_id);
          return <option key={item.account_id} value={item.account_id}>{item.currency} · {
            channel ? channelTitle({ id: channel.channel_id ?? channel.account_id, provider: channel.provider, name: channel.name }, t)
              + " · " + item.account_id : item.account_id
          }</option>;
        })}
      </select>
      {account && <p className="muted small">{t("Available {available} · Limit {limit}", { available: money(account.available_minor, account.currency), limit: money(account.max_payout_minor, account.currency) })}</p>}
      {account && <p className="muted small">{t("Merchant rate")} · {rateText(payoutQuote?.quote, account.currency, locale)}</p>}
      <label htmlFor="payout-recipient">{t("Recipient")}</label>
      <select id="payout-recipient" name="recipient_id" key={accountID} defaultValue="" required>
        <option value="" disabled>{account && !eligible.length ? t("Add a matching sandbox recipient") : t("Select a recipient")}</option>
        {eligible.map(item => <option key={item.id} value={item.id}>
          {t(types[item.type])} · {item.masked_destination} · {item.id.slice(-8)}
        </option>)}
      </select>
      <label htmlFor="payout-reference">{t("Merchant reference")}</label>
      <input id="payout-reference" name="merchant_reference" maxLength={80} pattern="[a-zA-Z0-9._:\-]{1,80}" required />
      <label htmlFor="payout-amount">{t("Amount in minor units")}</label>
      <input id="payout-amount" name="amount_minor" type="number" inputMode="numeric" min={1} max={account?.max_payout_minor} step={1} required />
    </InventoryForm>}
    {selected && <PayoutDetail id={selected} member={member} token={token} recipients={recipients.data ?? []}
      close={() => setSelected("")} updated={() => { payouts.reload(); accounts.reload(); }} />}
  </>;
}

function PayoutDetail({ id, token, member, recipients, close, updated }: {
  id: string; token: string; member: MemberSession; recipients: Recipient[]; close: () => void; updated: () => void;
}) {
  const { t, dateTime } = useLocale();
  const path = "/v1/payouts/" + encodeURIComponent(id);
  const detail = useWorkspaceData<Payout>(path, token);
  const pending = useRef(false);
  const [decision, setDecision] = useState<"approve" | "reject">();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function review(action: "approve" | "reject") {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      await request<Payout>(path + "/" + action, token, { method: "POST" });
    } catch (reason) {
      setError(reason instanceof ApiError && reason.status < 500 ? message(reason) : "The review outcome could not be confirmed. Check the current order status.");
    } finally {
      setDecision(undefined);
      detail.reload();
      updated();
      setBusy(false);
      pending.current = false;
    }
  }

  return <Dialog drawer title={t("Payout details")} close={close} locked={busy}>
    <LoadState state={detail} label={t("payout details")}>{item => <>
      <PayoutFacts item={item}>{[
        [t("Account"), item.account_id],
        ...(item.platform_channel_id ? [[t("Channel ID"), item.platform_channel_id] as [string, string]] : []),
        [t("Recipient"), [t(types[item.recipient_type]), recipients.find(recipient => recipient.id === item.recipient_id)?.masked_destination].filter(Boolean).join(" · ")],
        [t("Created by"), item.created_by], [t("Reviewed by"), item.reviewed_by ?? t("Awaiting review")],
        ...(item.reviewed_at ? [[t("Reviewed at"), dateTime(item.reviewed_at)] as [string, string]] : []),
        ...(item.channel_reference ? [[t("Channel reference"), item.channel_reference] as [string, string]] : []),
        ...(item.transfer_reference ? [[t("Transfer reference"), item.transfer_reference] as [string, string]] : []),
        ...(item.failure_code ? [[t("Reason"), t(reasons[item.failure_code] ?? item.failure_code)] as [string, string]] : []),
      ]}</PayoutFacts>
      {item.status === "draft" && item.created_by === member.id && <p className="muted">{t("A different admin must review this draft.")}</p>}
      <div className="detail-actions payout-actions">
        {item.status === "draft" && import.meta.env.MODE !== "community-demo" && member.role === "admin" && item.created_by !== member.id && (decision ? <>
          <button className="primary" disabled={busy} onClick={() => void review(decision)}>
            {decision === "approve" ? t("Confirm approval") : t("Confirm rejection")}
          </button>
          <button className="secondary" disabled={busy} onClick={() => setDecision(undefined)}>{t("Cancel review")}</button>
        </> : <>
          <button className="primary" onClick={() => setDecision("approve")}>{t("Approve payout")}</button>
          <button className="secondary" onClick={() => setDecision("reject")}>{t("Reject payout")}</button>
        </>)}
        <button className="secondary" disabled={busy} onClick={() => { detail.reload(); updated(); }}>{t("Refresh status")}</button>
      </div>
    </>}</LoadState>
    {error && <p className="error" role="alert">{t(error)}</p>}
  </Dialog>;
}
