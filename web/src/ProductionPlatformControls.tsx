import { useEffect, useRef, useState } from "react";
import { request, type FeeRate } from "./api";
import { channelTitle } from "./ChannelIdentity";
import { useLocale } from "./locale";
import { platformAPI } from "./platformEnvironment";
import { ControlAction } from "./WorkspaceControls";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";
import { ProductionInvitationResult } from "./PlatformMerchants";
import { RateFields, rateText, readRateFields, type PricingChannel, type Rates } from "./ChannelPricing";

export type RoutingScope = {
  channel_id: string; name: string; market: string; currency: string; method: string; direction: "collection" | "payout";
  min_amount_minor:string|null;max_amount_minor:string|null;
  permitted: boolean; configured: boolean; priced: boolean; funding_bound: boolean; routed: boolean; ready: boolean;
};
export type RouteFocus = Pick<RoutingScope, "channel_id" | "direction" | "currency">;

export type Administrator = { email?: string; status: "pending" | "expired" | "active" | "disabled" };

export function productionControl<T>(path: string, body: object) {
  return request<T>(platformAPI + path, undefined, { method: "POST", body: JSON.stringify(body) });
}

export function ProductionAdministrator({ merchant, admin, operator, refresh }: {
  merchant: string; admin?: Administrator; operator: boolean; refresh: () => void;
}) {
  const { t } = useLocale();
  const [editing, setEditing] = useState(false);
  const [issued, setIssued] = useState<{ email: string; initial_password: string }>();
  return <div>
    <h3>{t("Merchant workspace activation")}</h3>
    {admin?.email && <p className="wrap">{admin.email}</p>}
    {!admin && <p>{t("No administrator invitation record")}</p>}
    {admin && <p className="muted small">{t({ pending: "Awaiting first-time setup", expired: "Expired",
      active: "Active", disabled: "Disabled" }[admin.status])}</p>}
    {operator && (!admin || ["pending", "expired"].includes(admin.status)) && <button type="button" className="secondary"
      onClick={() => setEditing(true)}>{t(admin ? "Replace initial password" : "Generate initial password")}</button>}
    {editing && <ControlAction title={t(admin ? "Replace initial password" : "Generate initial password")}
      close={() => setEditing(false)} refresh={refresh} submitLabel={t("Generate initial password")}
      action={async fields => {
        const result = await productionControl<{ email: string; initial_password: string }>("/merchants/login", {
          merchant_id: merchant, email: fields.get("email"),
        });
        setIssued(result);
      }}>
      <label htmlFor="production-admin-email">{t("Administrator email")}</label>
      <input id="production-admin-email" type="email" name="email" required maxLength={254} defaultValue={admin?.email} readOnly={Boolean(admin)} />
      {admin && <p>{t("Replacing the password invalidates the previous first-time setup credentials.")}</p>}
    </ControlAction>}
    {issued && <ProductionInvitationResult email={issued.email} password={issued.initial_password}
      close={() => setIssued(undefined)} />}
  </div>;
}

type RoutingAction = { kind: "access"; scope: RoutingScope; rates?: Rates } | {
  kind: "switch"; scope: RoutingScope; from: RoutingScope; fromQuote: FeeRate; targetQuote: FeeRate;
};

export function ProductionRouting({ merchant, merchantName, channels, scopes, rates, operator, refresh, price, openFunding, focusRoute }: {
  merchant: string; merchantName?: string; channels: PricingChannel[]; scopes: RoutingScope[]; rates: Rates[]; operator: boolean; refresh: () => void;
  openFunding?: () => void;
  focusRoute?: RouteFocus;
  price: (channel: PricingChannel, direction?: RoutingScope["direction"]) => void;
}) {
  const locale = useLocale();
  const { t } = locale;
  const [assigning, setAssigning] = useState(false);
  const [action, setAction] = useState<RoutingAction>();
  const [target, setTarget] = useState("");
  const [savedAssignment, setSavedAssignment] = useState("");
  const [custom, setCustom] = useState(false);
  const assignedCard = useRef<HTMLElement>(null);
  const focusedSection = useRef<HTMLElement>(null);
  const routeTarget = useRef(focusRoute);
  useEffect(() => {
    if (focusRoute && focusedSection.current && routeTarget.current) {
      focusedSection.current.focus();
      routeTarget.current = undefined;
    }
  }, [focusRoute, scopes]);
  const assignmentVisible = scopes.some(scope => scope.channel_id === savedAssignment);
  useEffect(() => {
    if (assignmentVisible) assignedCard.current?.focus();
  }, [assignmentVisible, savedAssignment]);
  const hasAssignment = new Set(scopes.map(scope => scope.channel_id));
  const unassigned = new Map<string, PricingChannel[]>();
  for (const channel of channels) {
    if (hasAssignment.has(channel.id)) continue;
    const entries = unassigned.get(channel.id) ?? [];
    entries.push(channel);
    unassigned.set(channel.id, entries);
  }
  const direction = (value: RoutingScope["direction"]) => t(value === "collection" ? "Collection" : "Payout");
  const format = (scope: RoutingScope) => [scope.market, scope.currency, t(scope.method)].join(" · ");
  const sameScope = (current: RoutingScope, scope: RoutingScope) =>
    current.direction === scope.direction && current.market === scope.market &&
      current.currency === scope.currency && current.method === scope.method;
  function original(scope: RoutingScope) {
    const permitted = scopes.filter(current => current.permitted && sameScope(current, scope));
    return permitted.length === 1 ? permitted[0] : undefined;
  }
  const status = (scope: RoutingScope) => !scope.configured ? "Upstream configuration pending"
    : !scope.priced ? "Rates required" : !scope.permitted ? "Access disabled"
    : !scope.ready ? "Dispatcher not ready" : "Ready";
  const ratesFor = (scope: RoutingScope) => rates.find(row => row.channel_id === scope.channel_id && row.direction === scope.direction &&
    row.currency === scope.currency);
  function quote(scope: RoutingScope) {
    const rate = ratesFor(scope);
    return rate?.override ?? rate?.default;
  }
  const amountRange = (scope:RoutingScope) => [scope.min_amount_minor,scope.max_amount_minor].map(amount=>amount==null?"—":locale.exactMoney(amount,scope.currency,true)).join(" – ");
  const selected = action?.scope;
  const selectedRates = action?.kind === "access" ? action.rates : undefined;
  const enabling = action?.kind === "access" && !selected?.permitted;
  const text = (cn:string,en:string) => locale.locale === "zh-CN" ? cn : en;
  const switchLabel = (scope:RoutingScope) => scope.direction === "collection" ? t("Switch collection channel") : text("切换代付通道","Switch payout channel");
  const actionLabel = action?.kind === "switch" ? switchLabel(action.scope) : selected?.permitted ? t("Disable access") : t("Enable access");
  const byChannel = new Map<string, RoutingScope[]>();
  for (const scope of scopes) {
    const entries=byChannel.get(scope.channel_id) ?? [];
    entries.push(scope);
    byChannel.set(scope.channel_id,entries);
  }

  return <section className="workspace-panel platform-control-panel">
    <div className="heading panel-heading"><h2>{t("Channel assignments and routing")}</h2>
      {operator && unassigned.size > 0 && <button type="button" className="primary" onClick={() => { setTarget(""); setAssigning(true); }}>{t("Assign channel")}</button>}
    </div>
    <p className="muted panel-note">{text("分配上游后，在对应的代收或代付卡片确认费率并开通。","After assignment, confirm the rate and enable collection or payout on the channel card.")}</p>
    {assignmentVisible && <p role="status" className="panel-note">{text("通道已分配。选择要开通的方向并确认商户费率。","Channel assigned. Select a direction and confirm the merchant rate.")}</p>}
    <div className="product-routes" role="group" aria-label={t("Channel assignments and routing")}>
    {[...byChannel].map(([id,entries])=><article className="platform-card product-route-card" key={id} tabIndex={id === savedAssignment ? -1 : undefined} ref={id === savedAssignment ? assignedCard : undefined}>
      <header><h3>{entries[0].name}</h3><small className="resource-id muted">{id}</small></header>
      {entries.map(scope=>{
        const from = !scope.permitted ? original(scope) : undefined;
        const sourceQuote = from && quote(from);
        const nextQuote = quote(scope);
        const channel = channels.find(item => item.id === id && item.method === scope.method && item.currency === scope.currency);
        const focused = focusRoute?.channel_id === id && focusRoute.direction === scope.direction && focusRoute.currency === scope.currency;
        return <section className={focused ? "product-route-row target-route" : "product-route-row"} key={scope.direction+scope.currency}
          ref={focused ? focusedSection : undefined} tabIndex={focused ? -1 : undefined}>
          <div className="heading"><strong className={"product-direction "+scope.direction}>{scope.direction==="collection"?"↙ ":"↗ "}{direction(scope.direction)}</strong>
          <span className={"tag "+(status(scope) === "Ready" ? "green" : "gray")}>{scope.permitted ? (scope.ready ? text("可交易","Ready for orders") : text("已开通 · 待就绪","Enabled · Not ready")) : t("Access disabled")}</span></div>
          <p>{format(scope)}</p>
          <p className="muted small">{t("Amount limits")} · {amountRange(scope)}</p>
          {nextQuote&&<p className="muted small">{text("商户费率","Merchant rate")} · {rateText(nextQuote,scope.currency,locale)} · {t(nextQuote.source === "override" ? "Merchant-specific rate" : "Uses default rate")}</p>}
          {from&&<p className="muted small">{t("Current channel")} · {from.name}</p>}
          {(!scope.configured||!scope.priced)&&<p className="muted small">{t(status(scope))}</p>}
          {scope.configured && scope.priced && !scope.funding_bound && <div className="header-actions">
            <span className="muted small">{text("待绑定资金账户","Funding account required")}</span>
            {operator && openFunding && <button type="button" className="text-button" onClick={openFunding}>
              {text("查看资金账户绑定","Open funds bindings")}
            </button>}
          </div>}
          {(from || scope.permitted) && scope.funding_bound && !scope.ready &&
            <p className="muted small">{text("派单尚未就绪，请在平台财务核对通道状态。","Dispatch is not ready. Check channel status in Platform finance.")}</p>}
          <div className="header-actions">{channel && <button type="button" className="text-button" onClick={()=>price(channel,scope.direction)}>{t("Channel pricing")}</button>}
          {operator && (scope.permitted || (!from && scope.configured && scope.priced && nextQuote)) &&
           <button type="button" className={scope.permitted?"secondary":"primary"} onClick={()=>{ setCustom(false); setAction({kind:"access",scope,rates:ratesFor(scope)}); }}>{t(scope.permitted?"Disable access":"Enable access")}</button>}
          {operator && from && sourceQuote && nextQuote && <button type="button" className="primary" disabled={!scope.ready || !scope.configured || !scope.priced}
             onClick={()=>setAction({kind:"switch",scope,from,fromQuote:sourceQuote,targetQuote:nextQuote})}>{switchLabel(scope)}</button>}</div>
        </section>;
      })}
    </article>)}</div>
    {!scopes.length && <p className="muted empty">{t("No channels assigned.")}</p>}
    {assigning && <ControlAction title={t("Assign channel")} close={() => setAssigning(false)} refresh={refresh} submitLabel={t("Assign channel")}
      action={async fields => {
        const id = String(fields.get("channel_id"));
        await productionControl("/assignments", { merchant_id: merchant, channel_id: id });
        setSavedAssignment(id);
      }}>
      <label htmlFor="ops-channel">{t("Channel")}</label>
      <select id="ops-channel" name="channel_id" required data-dialog-focus value={target} onChange={event => setTarget(event.target.value)}>
        <option value="" disabled>{t("Select a channel")}</option>
        {[...unassigned].map(([id, entries]) =>
          <option value={id} key={id}>{channelTitle(entries[0], t)} · {entries.map(formatChannel).join(" / ")}</option>)}
      </select>
      {target && <p>{text("分配后可为代收和代付分别确认费率并开通。","After assignment, confirm rates and enable collection and payout separately.")}</p>}
    </ControlAction>}
    {action && selected && <ControlAction title={actionLabel} close={() => setAction(undefined)} refresh={refresh}
      submitLabel={actionLabel} action={async fields => {
        const scope = { merchant_id: merchant, channel_id: selected.channel_id, currency: selected.currency };
        if (action.kind === "switch") {
          await productionControl("/routing-switch", { ...scope, direction:selected.direction, from_channel_id: action.from.channel_id,
            expected_from_quote_version: action.fromQuote.version_id, expected_quote_version: action.targetQuote.version_id });
        } else {
          const rate = selectedRates?.override ?? selectedRates?.default;
          await productionControl("/routing-access", {
            ...scope, direction: selected.direction, enabled: !selected.permitted,
            expected_quote_version: enabling ? rate?.version_id : undefined,
            custom_rate: enabling && custom ? {
              ...readRateFields(fields, selected.currency), expected_version: selectedRates?.override_head ?? "0",
            } : undefined,
          });
        }
      }}>
      <Details fields={[
        [t("Amount limits"),amountRange(selected)], [t("Merchant"), merchantName ? `${merchantName} · ${merchant}` : merchant], [t("Direction"), direction(selected.direction)], [t("Applicability"), format(selected)],
        ...(action.kind === "switch" ? [[t("Current channel"), action.from.name + " · " + rateText(action.fromQuote, selected.currency, locale)] as [string, string]] : []),
        [t(action.kind === "switch" ? "Proposed channel" : "Channel"), selected.name + " · " + rateText(action.kind === "switch" ? action.targetQuote : selectedRates?.override ?? selectedRates?.default, selected.currency, locale)],
      ]} />
      {enabling && <>
        <label className="platform-checkbox"><input type="checkbox" checked={custom} onChange={event => setCustom(event.target.checked)} />
          {text("设置商户专属费率","Set a merchant-specific rate")}
        </label>
        {custom ? <RateFields currency={selected.currency} current={selectedRates?.override ?? selectedRates?.default} />
          : <p>{selectedRates?.override ? t("Merchant-specific rate") : t("Uses default rate")}</p>}
        <p>{text("开通后仍需资金账户和派单服务就绪，商户才能下单。","Orders can be accepted once the funding account and dispatcher are ready.")}</p>
      </>}
      <p>{t("This change applies to new orders. Existing orders keep their original channel and rate.")}</p>
    </ControlAction>}
  </section>;

  function formatChannel(channel: PricingChannel) {
    return [channel.market, channel.currency, t(channel.method), channel.directions?.map(direction).join(" / ")].filter(Boolean).join(" · ");
  }
}

type Confirmation = { id: string; currency: string; amount: string; transfer_reference: string; paid_on: string; confirmed_by: string };

export function ProductionSettlements({ merchant }: { merchant: string }) {
  const { t } = useLocale();
  const state = useWorkspaceData<Confirmation[]>(platformAPI + "/settlements?" + new URLSearchParams({ merchant_id: merchant }), "");
  return <section className="workspace-panel platform-control-panel">
    <div className="heading panel-heading"><h2>{t("Historical offline settlements")}</h2></div>
    <LoadState state={state} label={t("Historical offline settlements")}>{items => items.length
      ? <WorkspaceTable label={t("Historical offline settlements")} columns={[t("Paid on"), t("Transfer reference"), t("Amount"), t("Operator")]}>
        {items.map(item => <tr key={item.id}><td>{item.paid_on}</td><td>{item.transfer_reference}</td>
          <td>{item.amount} {item.currency}</td><td>{item.confirmed_by}</td></tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("No offline settlement confirmations.")}</p>}
    </LoadState>
  </section>;
}
