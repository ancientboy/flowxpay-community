import { useCallback, useState } from "react";
import { ApiError, request, type FeeRate, type OrderFee } from "./api";
import { channelTitle, type Provider } from "./ChannelIdentity";
import { useLocale } from "./locale";
import { base } from "./WorkspaceAccess";
import { ControlAction } from "./WorkspaceControls";
import { Details, LoadState, WorkspaceTable, useWorkspaceData } from "./WorkspaceInventoryUI";
import type { Site } from "./SiteCards";

import { platformAPI as ops, productionOperations } from "./platformEnvironment";
export const merchantQuotesPath = "/sandbox/workspace/channel-quotes";
const directions = { collection: "Collection", payout: "Payout" } as const;
type Direction = keyof typeof directions;
type Kind = "cost" | "default" | "override";
const priceLabels = { cost: "Upstream cost", default: "Default merchant rate", override: "Merchant-specific rate" };
export type PricingChannel = { id: string; name: string; provider: string; enabled: boolean; market: string; currency: string; method: string;
  environment?: string; directions?: Direction[] };
export type Rates = { channel_id: string; direction: Direction; currency: string; cost?: FeeRate; default: FeeRate | null; override: FeeRate | null; override_head?: string };
export type MerchantQuote = { account_id: string; site_id?: string; channel_id?: string; name?: string; provider: string; environment?: string;
  currency: string; direction: Direction; quote: FeeRate | null };

type Locale = ReturnType<typeof useLocale>;
export function rateText(rate: FeeRate | null | undefined, currency: string, { locale, t, exactMoney }: Locale) {
  if (!rate) return t("Rate unavailable");
  const percent = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 2 }).format(rate.bps / 10000);
  return t("{percent} + {fixed} per transaction", { percent, fixed: exactMoney(String(rate.fixed_minor), currency, productionOperations) });
}

function rateDigits(currency: string) {
  return productionOperations
    ? new Intl.NumberFormat("en-US", { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2
    : 2;
}

export function readRateFields(fields: FormData, currency: string) {
  function toMinor(name: string, digits: number, limit: number) {
    const [whole, fraction = "", ...extra] = String(fields.get(name) ?? "").split(".");
    const value = Number(whole + fraction.padEnd(digits, "0"));
    if (!/^[0-9]+$/.test(whole) || /[^0-9]/.test(fraction) || extra.length || fraction.length > digits || !Number.isSafeInteger(value) || value > limit) {
      throw new ApiError(400, "Enter a valid percentage and fixed fee using the displayed currency precision.", "invalid_rate");
    }
    return value;
  }
  return { bps: toMinor("bps", 2, 10000), fixed_minor: toMinor("fixed_minor", rateDigits(currency), 999999999999) };
}

export function RateFields({ currency, current }: { currency: string; current?: FeeRate | null }) {
  const { locale } = useLocale();
  const digits = rateDigits(currency);
  const scale = 10 ** digits;
  const zh = locale === "zh-CN";
  return <>
    <label htmlFor="rate-bps">{zh ? "费率（%）" : "Rate (%)"}</label>
    <input id="rate-bps" type="number" name="bps" required min={0} max={100} step={0.01} inputMode="decimal"
      defaultValue={current ? (current.bps / 100).toFixed(2) : undefined} placeholder="2.70" />
    <label htmlFor="rate-fixed">{zh ? `固定费用（${currency}/笔）` : `Fixed fee (${currency} per order)`}</label>
    <input id="rate-fixed" type="number" name="fixed_minor" required min={0} max={999999999999 / scale} step={1 / scale} inputMode="decimal"
      defaultValue={current ? (current.fixed_minor / scale).toFixed(digits) : undefined} placeholder={(0).toFixed(digits)} />
  </>;
}

export function OrderQuote({ quote, currency }: { quote?: OrderFee | null; currency: string }) {
  const locale = useLocale();
  if (!quote) return <>{locale.t("Rate unavailable")}</>;
  return <>{locale.exactMoney(String(quote.fee_minor), currency, productionOperations)}
    <span className="channel-subtitle muted small">{rateText(quote, currency, locale)} · {locale.t(priceLabels[quote.source])} · {locale.t("Version")} {quote.version_id}</span></>;
}

type WorkspaceQuote = Omit<MerchantQuote, "site_id" | "provider"> & Partial<Pick<MerchantQuote, "site_id" | "provider">> & {
  method?: string; permitted?: boolean; ready?: boolean; min_amount_minor?: number | null; max_amount_minor?: number | null;
};

export function MerchantRates({ token, siteRouting }: { token: string; siteRouting: boolean }) {
  const locale = useLocale();
  const { t, money } = locale;
  const quotes = useWorkspaceData<WorkspaceQuote[]>(base + "/channel-quotes", token);
  const sites = useWorkspaceData<Site[]>(siteRouting ? base + "/sites" : undefined, token);
  return <section className={"workspace-panel" + (siteRouting ? " merchant-rate-list site-rate-list" : " merchant-rate-list")}>
    <div className="heading panel-heading"><h2>{t("Your channel rates")}</h2></div>
    <LoadState state={quotes} label={t("Your channel rates")}>{items => items.length ?
      <WorkspaceTable label={t("Your channel rates")} columns={[
        ...(siteRouting ? [t("Site")] : []), t("Channel"), t("Direction"), t("Merchant rate"),
        ...(!siteRouting ? [t("Status"), t("Amount limits")] : []),
      ]}>
        {items.map(item => {
          const limits = item.min_amount_minor == null && item.max_amount_minor == null ? "—" :
            [item.min_amount_minor, item.max_amount_minor].map(value => value == null ? "—" : money(value, item.currency)).join(" – ");
          return <tr key={[item.account_id, item.direction, item.currency].join("/")}>
          {siteRouting && <td>{sites.data?.find(site => site.id === item.site_id)?.name ?? item.site_id}</td>}
          <td>{item.provider ? channelTitle({ id: item.channel_id ?? item.account_id, provider: item.provider, name: item.name }, t)
            : <>{t(item.name??"Channel")}<span className="resource-id account-id muted">{item.account_id}</span></>}</td>
          <td>{t(directions[item.direction])} · {item.currency}{item.method && <> · {t(item.method)}</>}</td>
          <td>{rateText(item.quote, item.currency, locale)}{item.quote && <span className="channel-subtitle muted small">
            {t(priceLabels[item.quote.source])} · {t("Version")} {item.quote.version_id}
          </span>}</td>
          {!siteRouting && <><td>{t(!item.permitted ? "Trading disabled" : item.ready ? "Ready for orders" : "Waiting for dispatch")}</td>
            <td><span className="rate-limit-value">{limits}</span>
              <details className="rate-limit-disclosure"><summary>{t("Amount limits")}</summary>{limits}</details></td></>}
        </tr>;})}
      </WorkspaceTable> : <p className="muted empty">{t(siteRouting ? "No active channel rates are available." : "No assigned channels.")}</p>}</LoadState>
  </section>;
}

export function ChannelPricePanel({ channel, token, merchant, operator, close, onPublished, selectedDirection }: {
  channel: PricingChannel; token: string; merchant: string; operator: boolean; close: () => void;
  onPublished: () => void; selectedDirection?: Direction;
}) {
  const locale = useLocale();
  const { t, dateTime } = locale;
  const scoped = merchant ? "/merchants/" + encodeURIComponent(merchant) : "";
  const state = useWorkspaceData<Rates[]>(productionOperations ? ops + "/prices?" + new URLSearchParams({ merchant })
    : ops + scoped + "/prices", token);
  const [conflict, setConflict] = useState(false);
  const [edit, setEdit] = useState<{ direction: Direction; kind: Kind; restore?: boolean; expected?: string; defaultVersion?: string }>();
  const kinds: Kind[] = [...(operator && !merchant ? ["cost" as const] : []), "default", ...(merchant ? ["override" as const] : [])];
  const shownDirections = selectedDirection ? [selectedDirection] : channel.directions ?? Object.keys(directions) as Direction[];
  return <section className="workspace-panel" aria-label={t("Channel pricing")}>
    <div className="heading panel-heading"><h2>{t("Channel pricing")} · {channelTitle(channel, t, true)}</h2>
      <button type="button" className="secondary" onClick={close}>{t("Close")}</button></div>
    <p className="muted panel-note">{t(merchant ? "Merchant overrides apply only to this merchant. Restore the default to follow future channel rate updates." :
      "Set the upstream cost and the default rate charged to merchants for each direction.")}</p>
    {conflict && <p role="alert">{t("The rate changed. Review the current version and reopen the editor.")}</p>}
    <LoadState state={state} label={t("Channel pricing")}>{rates => <>
      <WorkspaceTable label={t("Channel pricing")} columns={[t("Direction"), ...kinds.map(kind => t(kind === "override" ? "Effective merchant rate" : priceLabels[kind]))]}>
        {shownDirections.map(direction => {
          const entry = rates.find(row => row.channel_id === channel.id && row.direction === direction && row.currency === channel.currency);
          return <tr key={direction}><th scope="row">{t(directions[direction])} · {channel.currency}</th>
            {kinds.map(kind => {
              const rate = entry?.[kind];
              const effective = kind === "override" ? rate ?? entry?.default : rate;
              return <td key={kind}>
                {rateText(effective, channel.currency, locale)}
                {kind === "override" && effective && <span className={rate ? "tag green rate-source" : "tag blue rate-source"}>
                  {t(rate ? "Merchant-specific rate" : "Uses default rate")}</span>}
                {rate && <span className="channel-subtitle muted small">{t("Version")} {rate.version_id} · {dateTime(rate.effective_at)}</span>}
                {operator && Boolean(merchant) === (kind === "override") && <div className="header-actions">
                  <button className="text-button" onClick={() => { setConflict(false); setEdit({ direction, kind, expected: kind === "override" ? entry?.override_head : rate?.version_id }); }}>
                    {t("Edit {rate}", { rate: t(priceLabels[kind]) })}</button>
                  {kind === "override" && rate && <button className="text-button" onClick={() => { setConflict(false); setEdit({ direction, kind, restore: true, expected: entry?.override_head, defaultVersion: entry?.default?.version_id }); }}>
                    {t("Restore default rate")}</button>}
                </div>}
              </td>;
            })}
          </tr>;
        })}
      </WorkspaceTable>
      {productionOperations && shownDirections.map(direction => <ProductionRateHistory key={direction} token={token}
        channel={channel} merchant={merchant} direction={direction} revision={rates} operator={operator} />)}
      {edit && (() => {
        const row = rates.find(row => row.channel_id === channel.id && row.direction === edit.direction && row.currency === channel.currency);
        const current = row?.[edit.kind] ?? (edit.kind === "override" ? row?.default : null);
        const title = edit.restore ? "Restore default rate" : priceLabels[edit.kind];
        return <ControlAction title={t(title)} close={() => setEdit(undefined)} refresh={() => { state.refresh(); onPublished(); }}
          submitLabel={t(edit.restore ? "Restore default rate" : "Publish rate")}
          action={async fields => {
            const rate = edit.restore ? { restore_default: true } : readRateFields(fields, channel.currency);
            if (productionOperations) {
              try {
                await request(ops + "/rates", token, { method: "POST", body: JSON.stringify({
                  channel_id: channel.id, merchant_id: edit.kind === "override" ? merchant : "",
                  direction: edit.direction, currency: channel.currency, kind: edit.kind,
                  expected_version: edit.expected ?? "0", expected_default_version: edit.defaultVersion, ...rate,
                }) });
              } catch (reason) {
                if (reason instanceof ApiError && reason.code === "rate_version_changed") {
                  setEdit(undefined);
                  setConflict(true);
                  state.refresh();
                }
                throw reason;
              }
              return;
            }
            await request(ops + (edit.kind === "override" ? scoped : "") + "/channels/" + encodeURIComponent(channel.id) + "/prices",
              token, { method: "PUT", body: JSON.stringify({ direction: edit.direction, currency: channel.currency, kind: edit.kind, ...rate }) });
          }}>
          <p>{channelTitle(channel, t, true)} · {t(directions[edit.direction])} · {channel.currency}</p>
          <p>{t("The new rate applies to new orders after publication.")}</p>
          {!edit.restore && <RateFields currency={channel.currency} current={current} />}
        </ControlAction>;
      })()}
    </>}</LoadState>
  </section>;
}

type RateVersion = { version: string; kind: Kind; bps: number | null; fixed_minor: number | null; published_at: string; changed_by: string };

function ProductionRateHistory({ token, channel, merchant, direction, revision, operator }: {
  token: string; channel: PricingChannel; merchant: string; direction: Direction; revision: Rates[]; operator: boolean;
}) {
  const locale = useLocale();
  const { t, dateTime } = locale;
  const [pages, setPages] = useState([""]);
  const cursor = pages[pages.length - 1];
  const query = new URLSearchParams({ channel_id: channel.id, merchant_id: merchant, direction, currency: channel.currency, before: cursor });
  const load = useCallback(async (path: string, token?: string, init?: RequestInit) => {
    return request<{ history: RateVersion[]; next?: string }>(path, token, init);
  }, [revision]);
  const state = useWorkspaceData(ops + "/rates?" + query, token, load);

  function page(next: string[]) {
    state.reload();
    setPages(next);
  }

  return <details className="platform-advanced-rates"><summary>{t("Rate history")} · {t(directions[direction])} · {channel.currency}</summary>
    <LoadState state={state} label={t("Rate history")}>{data => <>
      <WorkspaceTable label={t("Rate history")} columns={[t("Version"), t("Rate type"), t("Rate"), t("Operator"), t("Created")]}>
        {data.history.filter(row => row.kind !== "cost" || (operator && !merchant)).map(row => <tr key={row.version}>
          <td>{row.version}</td><td>{t(priceLabels[row.kind])}</td>
          <td>{row.bps === null || row.fixed_minor === null ? t("Uses default rate") : rateText({
            bps: row.bps, fixed_minor: row.fixed_minor, version_id: row.version, effective_at: row.published_at, source: row.kind,
          }, channel.currency, locale)}</td>
          <td>{row.changed_by}</td><td>{dateTime(row.published_at)}</td>
        </tr>)}
      </WorkspaceTable>
      {pages.length > 1 && <button type="button" className="secondary" onClick={() => page(pages.slice(0, -1))}>{t("Previous page")}</button>}
      {data.next && <button type="button" className="secondary" onClick={() => page([...pages, data.next!])}>{t("Next page")}</button>}
    </>}</LoadState>
  </details>;
}

export function CatalogRateSummary({ channel, rates, operator }: { channel: PricingChannel; rates: Rates[]; operator: boolean }) {
  const locale = useLocale();
  const { t } = locale;
  return <dl className="platform-card-meta catalog-rates">
    {(channel.directions ?? Object.keys(directions) as Direction[]).map(direction => {
      const rate = rates.find(item => item.channel_id === channel.id && item.direction === direction && item.currency === channel.currency);
      return <div key={direction}><dt>{t(directions[direction])} · {channel.currency}</dt><dd>
        {operator && <span>{t("Upstream cost")}: {rateText(rate?.cost, channel.currency, locale)}</span>}
        <span>{t("Default merchant rate")}: {rateText(rate?.default, channel.currency, locale)}</span>
      </dd></div>;
    })}
  </dl>;
}

export function MerchantChannelRates({ quotes, sites, open }: {
  quotes: MerchantQuote[]; sites: Site[]; open: (quote: MerchantQuote) => void;
}) {
  const locale = useLocale();
  const { t } = locale;
  return <section className="workspace-panel platform-control-panel" aria-label={t("Merchant channel rates")}>
    <div className="heading panel-heading"><h2>{t("Merchant channel rates")}</h2></div>
    <p className="muted panel-note">{t("These are the rates for this merchant’s available collection and payout channels.")}</p>
    {quotes.length ? <WorkspaceTable label={t("Merchant channel rates")}
      columns={[...(!productionOperations ? [t("Site")] : []), t("Channel"), t("Direction"), t("Effective merchant rate"), t("Action")]}>
      {quotes.map(quote => <tr key={quote.account_id + ":" + quote.direction + ":" + quote.currency}>
        {!productionOperations && <td>{sites.find(site => site.id === quote.site_id)?.name ?? quote.site_id}</td>}
        <td>{channelTitle({ id: quote.channel_id ?? quote.account_id, name: quote.name, provider: quote.provider, environment: quote.environment }, t, true)}</td>
        <td>{t(directions[quote.direction])} · {quote.currency}</td>
        <td>{rateText(quote.quote, quote.currency, locale)}{quote.quote && <span className={quote.quote.source === "override" ? "tag green rate-source" : "tag blue rate-source"}>
          {t(quote.quote.source === "override" ? "Merchant-specific rate" : "Uses default rate")}</span>}</td>
        <td>{quote.channel_id && <button type="button" className="text-button" onClick={() => open(quote)}>{t("Channel pricing")}</button>}</td>
      </tr>)}
    </WorkspaceTable> : <p className="muted empty">{t("There are no available collection or payout channels for this merchant.")}</p>}
  </section>;
}

type SwitchChannel = { channel_id?: string; account_id?: string; name?: string; provider: Provider; available: boolean;
  cost?: FeeRate; quote: FeeRate | null };
type SwitchPreview = { preview: string; currency: string; market: string; method: string; current: SwitchChannel | null; target: SwitchChannel };

export function QuickChannelSwitch({ sites, initialSite, channels, current, preview }: {
  sites: Site[]; initialSite?: string;
  channels: { siteID: string; value: PricingChannel }[];
  current: { siteID: string; scope: string; channel: string }[]; preview: (site: Site, channel: PricingChannel) => void;
}) {
  const { t } = useLocale();
  const [siteID, selectSite] = useState(initialSite ?? sites.find(site => site.is_default)?.id ?? (sites.length === 1 ? sites[0].id : ""));
  const site = sites.find(item => item.id === siteID);
  const groups = new Map<string, PricingChannel[]>();
  for (const { siteID: assignedSite, value } of channels) {
    if (assignedSite !== siteID) continue;
    const group = [value.market, value.currency, value.method].join("/");
    const list = groups.get(group);
    if (list) list.push(value);
    else groups.set(group, [value]);
  }
  if (!sites.length) return null;
  return <section className="workspace-panel platform-control-panel">
    <div className="heading panel-heading"><h2>{t("Switch collection channel")}</h2></div>
    <div className="quick-channel-switch">
      <label htmlFor="quick-site">{t("Site")}</label>
      <select id="quick-site" value={siteID} onChange={event => selectSite(event.currentTarget.value)}>
        <option value="" disabled>{t("Select a site")}</option>
        {sites.map(s => <option key={s.id} value={s.id}>{s.name}
          {sites.some(other => other.id !== s.id && other.name === s.name) ? " · " + s.id : ""}</option>)}
      </select>
      {site && !groups.size && <p className="muted" role="status">{t("No eligible channels match these filters.")}</p>}
      {[...groups].map(([scope, candidates]) => {
        const { market, currency, method } = candidates[0];
        const active = current.find(item => item.siteID === siteID && item.scope === scope);
        return <section className="quick-channel-group" key={scope}>
          <h3>{market} · {currency} · {t(method)}</h3>
          <p className="muted small">{t("Current channel")} · {active?.channel ?? t("No active channel")}</p>
          <div className="header-actions">
            {candidates.map(channel => <button type="button" key={channel.id} className="secondary wrap" data-channel={channel.id} onClick={() => { if (site) preview(site, channel); }}>
              {t("Preview {channel}", { channel: channelTitle(channel, t) })}
            </button>)}
          </div>
        </section>;
      })}
    </div>
  </section>;
}

export function SwitchConfirmation({ token, merchant, site, channel, close, refresh }: {
  token: string; merchant: string; site: Site; channel: PricingChannel; close: () => void; refresh: () => void;
}) {
  const locale = useLocale();
  const { t } = locale;
  const path = ops + "/merchants/" + encodeURIComponent(merchant) + "/sites/" + encodeURIComponent(site.id) + "/switch/" + encodeURIComponent(channel.id);
  const state = useWorkspaceData<SwitchPreview>(path, token);
  return <ControlAction title={t("Review collection channel switch")} close={close} refresh={refresh}
    submitLabel={t("Confirm switch")} unavailable={!state.data?.preview || Boolean(state.error)}
    action={async () => {
      await request(path, token, { method: "POST", body: JSON.stringify({ preview: state.data?.preview }) });
    }}>
    <LoadState state={state} label={t("Switch preview")}>{value => <>
      <p>{site.name} · {value.market} · {value.currency} · {t(value.method)}</p>
      <div className="content-grid">
        {([[t("Current channel"), value.current], [t("Target channel"), value.target]] as const).map(([label, item]) =>
          <section key={label}><h3>{label}</h3>{item ? <Details fields={[
            [t("Channel"), channelTitle({ id: item.channel_id ?? item.account_id ?? "", provider: item.provider, name: item.name }, t)],
            [t("Status"), t(item.available ? "Enabled" : "Disabled")],
            [t("Merchant rate"), rateText(item.quote, value.currency, locale)],
            [t("Upstream cost"), rateText(item.cost, value.currency, locale)],
          ]} /> : <p>{t("No active channel")}</p>}</section>)}
      </div>
      {!value.target.account_id && <p>{t("Confirmation also assigns the target channel to this site.")}</p>}
      <p>{t("This route applies to new orders immediately. Existing orders keep their original route.")}</p>
    </>}</LoadState>
  </ControlAction>;
}

type OrderFeeRow = {
  id: string; merchant_reference?: string; account_id: string; direction: Direction; currency: string; status: string; amount_minor: number;
  quote: OrderFee | null; cost?: OrderFee; estimated_margin_minor?: number; actual_channel_cost_minor?: number; matched_margin_minor?: number;
  statement_fee_minor: number | null; statement_net_minor: number | null; statement_batch_id?: string; statement_status: string;
};
const orderStatuses: Record<string, string> = {
  queued: "Queued", routing: "Processing", pending_approval: "Pending approval", cancelled: "Cancelled",
  requires_action: "Awaiting payment", succeeded: "Succeeded", expired: "Expired",
  pending_confirmation: "Pending confirmation", failed: "Failed", rejected: "Rejected",
  processing: "Processing", draft: "Awaiting review",
};
const statementStatuses: Record<string, string> = { missing: "No matching statement", reversal_reported: "Reversal reported", refund_reported: "Refund reported",
  unmatched: "Statement discrepancy", pending: "Statement pending", matched_settled: "Matched settled statement" };

export function PlatformOrderFees({ token, merchant, siteID, operator }: {
  token: string; merchant: string; siteID?: string; operator: boolean;
}) {
  const { t, exactMoney } = useLocale();
  const [reference, setReference] = useState("");
  const query = new URLSearchParams({ site_id: siteID ?? "", reference });
  const state = useWorkspaceData<OrderFeeRow[]>(productionOperations ? ops + "/order-fees?" + new URLSearchParams({ merchant, reference }) :
    ops + "/merchants/" + encodeURIComponent(merchant) + "/order-fees?" + query, token);
  return <section className="workspace-panel platform-control-panel order-fee-panel">
    <div className="heading panel-heading"><h2>{t("Order fees and reconciliation")}</h2>
      <button className="secondary" onClick={state.refresh}>{t("Refresh")}</button></div>
    <p className="muted panel-note">{t(operator
      ? (productionOperations ? "Matched margin requires an attributed upstream cost on a settled statement." : "Estimated gross margin uses the order’s merchant fee minus estimated channel cost.")
      : "Statements report fee and net amounts for matched settled entries.")}</p>
    <form className="overview-filters payment-reference-filter" onSubmit={event => {
      event.preventDefault();
      setReference(String(new FormData(event.currentTarget).get("reference")).trim());
    }}>
      <input id="order-fee-reference" name="reference" aria-label={t("Order reference or ID")}
        placeholder={t("Order reference or ID")} maxLength={80} pattern="[a-zA-Z0-9._:\-]{1,80}" />
      <button className="secondary">{t("Search")}</button>
    </form>
    <LoadState state={state} label={t("Order fees and reconciliation")}>{items => items.length ?
      <WorkspaceTable label={t("Order fees and reconciliation")} columns={[t("Order"), t("Quoted merchant fee"),
        ...(operator ? [t("Estimated channel cost"), t("Estimated gross margin"), ...(productionOperations ? [t("Actual channel cost"), t("Matched order margin")] : [])] : []), t("Reconciliation")]}>
        {items.map(row => {
          const money = (value: number | null | undefined) => value == null ? t("Data unavailable") : exactMoney(String(value), row.currency, productionOperations);
          return <tr key={row.id}>
            <td className="wrap"><span className="order-number">{row.id}</span>
              <span className="channel-subtitle muted small">{t(directions[row.direction])} · {money(row.amount_minor)} · {t(orderStatuses[row.status] ?? row.status)}</span>
              <span className="resource-id account-id muted">{row.merchant_reference && row.merchant_reference + " · "}{row.account_id}</span>
            </td>
            <td><OrderQuote quote={row.quote} currency={row.currency} /></td>
            {operator && <><td>{money(row.cost?.fee_minor)}{row.cost && <span className="channel-subtitle muted small">{t("Version")} {row.cost.version_id}</span>}</td>
              <td>{money(row.estimated_margin_minor)}</td>
              {productionOperations && <><td>{money(row.actual_channel_cost_minor)}</td><td>{money(row.matched_margin_minor)}</td></>}</>}
            <td>{t(statementStatuses[row.statement_status])}
              {row.statement_fee_minor != null && <div className="small">{t("Statement fee")} {money(row.statement_fee_minor)}
                <br />{t("Statement net")} {money(row.statement_net_minor)}</div>}
              {row.statement_batch_id && <span className="resource-id account-id muted">{row.statement_batch_id}</span>}
            </td>
          </tr>;
        })}
      </WorkspaceTable> : <p className="muted empty">{t("No orders match these filters.")}</p>}</LoadState>
  </section>;
}
