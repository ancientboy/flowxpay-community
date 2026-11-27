import {ChannelCycle,type Cycle} from "./ChannelCycle";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, request } from "./api";
import { useLocale } from "./locale";
import { channelProfiles, channelSubtitle, channelTitle, ChannelTechnical, type Provider } from "./ChannelIdentity";
import { collectionAccountStatus, type ChannelAccount } from "./MemberWorkspace";
import { SiteCards, type Site, type SiteCard } from "./SiteCards";
import { ControlAction, routeStatus, type APIKey, type IssuedKey, type RouteChange } from "./WorkspaceControls";
import { Details, InventoryForm, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";
import { MerchantAdminInvitation, type AdminInvitation } from "./PlatformMerchants";
import { CatalogRateSummary, ChannelPricePanel, MerchantChannelRates, PlatformOrderFees, QuickChannelSwitch, SwitchConfirmation, type Rates, type MerchantQuote, type PricingChannel } from "./ChannelPricing";
import { PlatformChannelCards } from "./PlatformChannelCards";
import { platformTeamPath, type PlatformTeamMember } from "./PlatformTeam";
import type { PlatformSession, PlatformTask } from "./PlatformWorkspace";

import { platformAPI as base, productionOperations } from "./platformEnvironment";
import { ProductionAdministrator, ProductionRouting, productionControl, type Administrator, type RoutingScope, type RouteFocus } from "./ProductionPlatformControls";
interface Channel extends PricingChannel {
  configured?: boolean;
  limits?: { direction: MerchantQuote["direction"]; min: number | null; max: number | null }[];
}
type Applicability = Pick<Channel, "market" | "currency" | "method">;
type Choice = Applicability & { id: string; label: string; storedName?: string; siteId?: string; assignedSites?: string[] };

function formatApplicability(value: Applicability, t: ReturnType<typeof useLocale>["t"]) {
  return [value.market, value.currency, t(value.method)].join(" · ");
}

function applicabilityKey({ market, currency, method }: Applicability) {
  return [market, currency, method].join("/");
}

function ChannelTemplateFields() {
  const { t } = useLocale();
  const [provider, setProvider] = useState<Provider | "">("");
  return <>
    <label htmlFor="ops-profile">{t("Mock channel template")}</label>
    <select id="ops-profile" name="provider" data-dialog-focus required value={provider}
      onChange={event => setProvider(event.target.value as typeof provider)}>
      <option value="" disabled>{t("Select a mock channel template")}</option>
      {Object.entries(channelProfiles).map(([id, profile]) => <option key={id} value={id}>
        {t(profile.title)} · {formatApplicability(profile, t)}
      </option>)}
    </select>
    <label htmlFor="ops-channel-name">{t("Channel name (optional)")}</label>
    <input id="ops-channel-name" name="name" maxLength={100}
      placeholder={provider ? t(channelProfiles[provider].title) : undefined} />
    <label className="platform-checkbox"><input type="checkbox" name="enabled" defaultChecked /> {t("Enabled")}</label>
  </>;
}

function ChannelChoices({ sites, choices, route, initialSite }: {
  sites: Site[]; choices: Choice[]; route?: boolean; initialSite?: string;
}) {
  const { t } = useLocale();
  const [site, setSite] = useState(() => {
    if (initialSite) return sites.some(item => item.id === initialSite) ? initialSite : "";
    return sites.length === 1 ? sites[0].id : "";
  });
  const [search, setSearch] = useState("");
  const [scope, setScope] = useState("");
  const [selected, setSelected] = useState("");
  const candidates = choices.filter(choice => route ? choice.siteId === site : !choice.assignedSites?.includes(site));
  const scopes = new Map(candidates.map(choice => [applicabilityKey(choice), formatApplicability(choice, t)]));
  const filtered = candidates.filter(choice => {
    const applicability = formatApplicability(choice, t);
    return (!scope || applicabilityKey(choice) === scope) &&
      [choice.label, choice.storedName, applicability].join(" ").toLowerCase().includes(search.trim().toLowerCase());
  });
  const selectedChoice = filtered.find(choice => choice.id === selected);
  const id = route ? "ops-account" : "ops-channel";

  return <>
    <label htmlFor="ops-site">{t("Site")}</label>
    <select id="ops-site" name="site_id" required data-dialog-focus value={site} onChange={event => {
      setSite(event.target.value);
      setScope("");
      setSelected("");
    }}>
      <option disabled value="">{t("Select a site")}</option>
      {sites.map(item => <option key={item.id} value={item.id}>
        {item.name}{sites.some(other => other.id !== item.id && other.name === item.name) ? " · " + item.id : ""}
      </option>)}
    </select>
    <label htmlFor="ops-choice-scope">{t("Country, currency and payment method")}</label>
    <select id="ops-choice-scope" value={scope} disabled={!scopes.size} onChange={event => {
      setScope(event.target.value);
      setSelected("");
    }}>
      <option value="">{t("All supported combinations")}</option>
      {[...scopes].sort().map(([key, label]) => <option key={key} value={key}>{label}</option>)}
    </select>
    <label htmlFor="ops-choice-search">{t("Filter by channel name")}</label>
    <input id="ops-choice-search" type="search" value={search} onChange={event => {
      setSearch(event.target.value);
      setSelected("");
    }} />
    <label htmlFor={id}>{t(route ? "Channel account" : "Channel")}</label>
    <select id={id} name={route ? "account_id" : "channel_id"} required value={selectedChoice?.id ?? ""}
      aria-describedby={filtered.length ? undefined : "ops-choice-empty"} onChange={event => setSelected(event.target.value)}>
      <option disabled value="">{t(route ? "Select an account" : "Select a channel")}</option>
      {filtered.map(item => <option key={item.id} value={item.id}>
        {item.label}
      </option>)}
    </select>
    {!filtered.length && <p id="ops-choice-empty" className="muted small" role="status">
      {t(route && !site ? "Select a site to see its eligible accounts." : "No eligible channels match these filters.")}
    </p>}
    {selectedChoice && <p className="choice-summary muted small wrap">{selectedChoice.label}</p>}
  </>;
}
const actions = {
  "channel.create": "Channel created", "channel.status": "Channel status changed", "channel.assign": "Channel assigned",
  "route.submit": "Route proposed", "route.approve": "Route activated", "route.cancel": "Proposal cancelled",
  "route.switch": "Collection channel switched", "price.publish": "Channel rate published",
  "key.issue": "Key issued", "key.revoke": "Key revoked",
  "catalog.merchant_registered": "Merchant account opened", "catalog.channel_assigned": "Channel assigned",
  "merchant_login.provisioned": "Initial password issued", "merchant_key.issued": "Key issued", "merchant_key.revoked": "Key revoked",
  "routing.enabled": "Routing access enabled", "routing.disabled": "Routing access disabled",
  "rate.publish_cost": "Upstream cost published", "rate.publish_default": "Default merchant rate published",
  "rate.publish_override": "Merchant-specific rate published", "rate.restore_default": "Default rate restored",
  "settlement.confirmed_offline": "Offline settlement recorded",
  "fund.route_bound": "Funds account bound",
  "settlement.locked": "Settlement batch locked",
  "settlement.confirmed": "Offline transfer confirmed",
  "settlement.cancelled": "Settlement batch cancelled",
  "settlement.refund_posted": "Statement refund posted",
  "settlement.refund_recovered": "Refund reversal posted",
  "settlement.refund_historical": "Historical review completed",
  "settlement.refund_corrected": "Corrected by statement",
  "auth.platform_owner_assigned_by_db_admin": "Platform owner assigned",
  "platform_member.invited": "Platform member invited", "platform_member.disabled": "Platform member disabled",
  "auth.login": "Signed in", "auth.logout": "Signed out", "auth.setup_completed": "First-time setup",
  "merchant.onboard": "Merchant account opened", "merchant.invite_reissue": "Administrator invitation replaced",
} as const;
interface AuditEntry {
  id: number | string; occurred_at: string; actor: string; action: string; merchant_id?: string; resource_id: string;
  provider?: Provider; account_id?: string; platform_channel_id?: string; platform_channel_name?: string;
}
type EffectiveRoute = Applicability & { site_id: string; account_id: string };
interface UpstreamIntegration {
  id: string; name: string; market: string; currency: string; collections: boolean; payouts: boolean;
  validation_status: "pending_verification" | "signature_rejected";
}
interface Operations {
  channels: Channel[]; audit: AuditEntry[]; onboarding?: AdminInvitation; upstream_integrations?: UpstreamIntegration[];
  sites?: (Site | SiteCard)[]; accounts?: ChannelAccount[]; routes?: RouteChange[]; effective_routes?: EffectiveRoute[]; keys?: APIKey[];
  merchant_channel_quotes?: MerchantQuote[];
  administrator?: Administrator; routing?: RoutingScope[];
}
type Action =
  | { type: "channel" | "assign" | "propose" | "issue" }
  | { type: "status"; channel: Channel }
  | { type: "switch"; channel: Channel; site: Site }
  | { type: "approve" | "cancel"; route: RouteChange }
  | { type: "revoke"; key: APIKey };

export function PlatformOperations({ session, merchant, merchantName, task, siteID, openFunding, focusRoute, catalogOnly = false }: {
  session: PlatformSession; merchant: string; merchantName?: string; task?: PlatformTask; siteID?: string; catalogOnly?: boolean; openFunding?: () => void; focusRoute?: RouteFocus;
}) {
  const { t, exactMoney,locale } = useLocale();
  const state = useWorkspaceData<Operations>(base + "/operations?" + new URLSearchParams({ merchant }), session.token);
  const [selectedAction, setAction] = useState<Action | null>();
  const routeRow = useRef<HTMLTableRowElement>(null);
  const [pricing, setPricing] = useState<{ channel: Channel; direction?: MerchantQuote["direction"] }>();
  const catalogRates = useWorkspaceData<Rates[]>(base + "/prices" +
    (productionOperations && merchant ? "?" + new URLSearchParams({ merchant }) : ""), session.token);
  const cycles=useWorkspaceData<Cycle[]>(productionOperations && !merchant ? base+"/funds?part=cycles" : undefined,session.token);
  const catalogLabel = productionOperations ? "Upstream channels" : "Mock channel configuration";
  const pricingPanel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (pricing) pricingPanel.current?.scrollIntoView({ block: "start" });
  }, [pricing]);
  const [scopeCleared, setScopeCleared] = useState(false);
  const selectedSiteID = scopeCleared ? undefined : siteID;
  const routeID = scopeCleared ? undefined : task?.route_id;
  useEffect(() => {
    if (routeID && task?.kind !== "can_approve") routeRow.current?.focus();
  }, [routeID, state.data, task?.kind]);
  const canWrite = session.role === "operator";
  const close = () => setAction(null);
  const scoped = "/merchants/" + encodeURIComponent(merchant);

  const send = <T,>(path: string, body: object, method = "POST") =>
    request<T>(base + path, session.token, { method, body: JSON.stringify(body) });

  return <LoadState state={state} label={t("Operations")}>{data => {
    const sites = data.sites ?? [];
    const accounts = data.accounts ?? [];
    const routes = data.routes ?? [];
    const effective = data.effective_routes ?? [];
    const pending = routes.filter(route => routeStatus(route) === "Awaiting approval");
    const siteName = (id: string) => sites.find(site => site.id === id)?.name ?? id;
    const accountFor = (route: { account_id: string }) => accounts.find(account => account.id === route.account_id);
    const sameScope = (a: EffectiveRoute, b: EffectiveRoute) =>
      a.site_id === b.site_id && applicabilityKey(a) === applicabilityKey(b);
    const eligible = accounts.filter(account => account.enabled && account.environment === "sandbox");
    const canApprove = (route: RouteChange) => canWrite &&
      pending.some(item => item.id === route.id) && eligible.some(account =>
        account.id === route.account_id && account.site_id === route.site_id &&
        applicabilityKey(channelProfiles[account.provider]) === applicabilityKey(route));
    const proposed = pending.find(route => route.id === task?.route_id);
    const action = selectedAction === undefined && task?.kind === "can_approve" && proposed && canApprove(proposed)
      ? { type: "approve" as const, route: proposed } : selectedAction;
    const profile = (value: Applicability) => formatApplicability(value, t);
    const refresh = () => { state.refresh(); if (productionOperations) catalogRates.refresh(); };
    const availableChannels = data.channels.filter(channel => channel.enabled).map(channel => ({
      ...channel, storedName: channel.name, label: [channelTitle(channel, t), profile(channel), t("Sandbox")].join(" · "),
      assignedSites: accounts.filter(account => account.platform_channel_id === channel.id).map(account => account.site_id),
    }));
    const shownSites = sites.filter(site => !selectedSiteID || site.id === selectedSiteID);
    const assignmentSites = shownSites.filter(site => availableChannels.some(channel => !channel.assignedSites.includes(site.id)));
    const routeOptions = eligible.filter(account => (!selectedSiteID || account.site_id === selectedSiteID) && !pending.some(route =>
      route.site_id === account.site_id && applicabilityKey(route) === applicabilityKey(channelProfiles[account.provider])));

    const switchChannels = shownSites.flatMap(site => availableChannels.filter(channel => {
      const existing = accounts.find(account => account.site_id === site.id && account.platform_channel_id === channel.id);
      return (!existing || (existing.enabled && !effective.some(route => route.account_id === existing.id))) &&
        !pending.some(route => route.site_id === site.id && applicabilityKey(route) === applicabilityKey(channel));
    }).map(value => ({ siteID: site.id, value })));

    function channelSummary(value: Channel | ChannelAccount, account = false) {
      return <>{channelTitle(value, t)}<span className="channel-subtitle muted small">{channelSubtitle(value.provider, t)}</span>
        <ChannelTechnical value={value} account={account} /></>;
    }

    function routeAccount(account: ChannelAccount | undefined) {
      return account ? <span className="wrap">{channelTitle(account, t)}{!account.enabled && " · " + t("Disabled")}</span>
        : t("No active channel");
    }

    function dialog() {
      if (!action) return null;
      const shared = { close, refresh };
      switch (action.type) {
        case "channel":
          return <InventoryForm token={session.token} path={base + "/channels"} title={t("Configure mock channel")}
            submitLabel="Create channel" close={close} created={() => { refresh(); close(); }}
            payload={fields => ({ name: fields.get("name"), provider: fields.get("provider"), enabled: fields.has("enabled") })}>
            <ChannelTemplateFields />
          </InventoryForm>;
        case "switch":
          return <SwitchConfirmation {...shared} token={session.token} merchant={merchant} site={action.site} channel={action.channel} />;
        case "status": {
          const { channel } = action;
          const label = channel.enabled ? "Disable channel" : "Enable channel";
          return <ControlAction {...shared} title={t(label)} submitLabel={t(label)}
            action={async () => { await send("/channels/" + encodeURIComponent(channel.id) + "/status", { enabled: !channel.enabled }, "PUT"); }}>
            <p>{channelTitle(channel, t)} · {profile(channel)}</p>
            <p>{t("Channel status applies to new orders at every assigned site.")}</p>
          </ControlAction>;
        }
        case "assign":
          return <ControlAction {...shared} title={t("Assign channel to site")} submitLabel={t("Assign channel")}
            action={async fields => { await send(scoped + "/assignments", { site_id: fields.get("site_id"), channel_id: fields.get("channel_id") }); }}>
            <p className="wrap">{merchantName ?? merchant}</p>
            <ChannelChoices sites={assignmentSites} initialSite={selectedSiteID} choices={availableChannels} />
          </ControlAction>;
        case "propose":
          return <ControlAction {...shared} title={t("Configure collection route")} submitLabel={t("Save and activate")}
            action={async fields => {
              const account = routeOptions.find(item => item.id === fields.get("account_id") && item.site_id === fields.get("site_id"));
              if (!account) throw new ApiError(400, "Select a channel account.");
              await send(scoped + "/routes", { site_id: account.site_id, account_id: account.id, activate: true });
            }}>
            <p>{t("This route applies to new orders immediately. Existing orders keep their original route.")}</p>
            <ChannelChoices route initialSite={selectedSiteID} sites={sites.filter(site => routeOptions.some(account => account.site_id === site.id))}
              choices={routeOptions.map(account => {
                const scope = channelProfiles[account.provider];
                return { ...scope, id: account.id, siteId: account.site_id, storedName: account.platform_channel_name ?? scope.template,
                  label: [channelTitle(account, t), profile(scope), t(collectionAccountStatus(account, effective.some(route => route.account_id === account.id)))].join(" · ") };
              })} />
          </ControlAction>;
        case "approve":
        case "cancel": {
          const { route, type } = action;
          const target = accountFor(route);
          const current = effective.find(item => sameScope(item, route)) ?? routes.find(item =>
            sameScope(item, route) && routeStatus(item) === "In effect");
          const title = type === "approve" ? "Activate pending route" : "Cancel proposal";
          return <ControlAction {...shared} title={t(title)} submitLabel={t(type === "approve" ? "Save and activate" : title)}
            action={async () => { await send(scoped + "/routes/" + encodeURIComponent(route.id) + "/" + type, {}); }}>
            <div className="route-approval-summary"><Details fields={[
              [t("Merchant"), merchantName ?? merchant], [t("Site"), siteName(route.site_id)],
              [t("Applicability"), profile(route)], [t("Current channel"), routeAccount(current ? accountFor(current) : undefined)],
              [t("Proposed channel"), target ? routeAccount(target) : t("Unavailable")],
              [t("Submitted by"), route.submitted_by],
            ]} /></div>
            {target && <ChannelTechnical value={target} account resource={route.id} />}
          </ControlAction>;
        }
        case "issue":
          if (productionOperations) return <ControlAction {...shared} title={t("Create production API key")} submitLabel={t("Create key")} productionKey
            action={async fields => {
              const apiKey = await productionControl<{ id: string; token: string; private_key_pem: string }>("/keys", {
                merchant_id: merchant, generate_key_pair: true, write_scope:fields.get("scope"),
              });
              return { id: apiKey.id, secret: apiKey.token, credentials: { merchant, apiKey }, label: "Production API key", platform: true, legacy: false };
            }}>
            <p>{merchantName ?? merchant} · {merchant}</p>
            <label>{locale==="zh-CN"?"密钥写入权限":"Key write permissions"}<select name="scope" defaultValue="collection"><option value="collection">{t("Collections")}</option><option value="payout">{t("Payouts")}</option><option value="read_only">{locale==="zh-CN"?"仅查询":"Read only"}</option><option value="both">{locale==="zh-CN"?"代收和代付":"Collections and payouts"}</option></select></label>
            <p>{t("Create a token and signing key pair for this merchant. Download the credentials after creation.")}</p>
          </ControlAction>;
          return <ControlAction {...shared} title={t("Create sandbox API key")} submitLabel={t("Create key")}
            action={fields => send<IssuedKey>(scoped + "/api-keys", { label: fields.get("label") })}>
            <p>{t("Merchant ID")} · {merchant}</p>
            <label htmlFor="ops-key-label">{t("Key label")}</label>
            <input id="ops-key-label" name="label" required maxLength={100} />
          </ControlAction>;
        case "revoke":
          return <ControlAction {...shared} title={t("Revoke API key")} submitLabel={t("Confirm revocation")}
            action={async () => {
              if (productionOperations) await productionControl("/keys/revoke", { merchant_id: merchant, id: action.key.id });
              else await send(scoped + "/api-keys/" + encodeURIComponent(action.key.id) + "/revoke", {});
            }}>
            <p>{t("The key will immediately stop authorizing new API requests.")}</p>
            <p className="wrap">{merchant} · {action.key.label} · {action.key.id}</p>
          </ControlAction>;
      }
    }

    const availableRates = catalogRates.data;
    const merchantQuotes = productionOperations ? (data.routing ?? []).map(scope => {
      const rate = availableRates?.find(item => item.channel_id === scope.channel_id && item.direction === scope.direction && item.currency === scope.currency);
      return { account_id: scope.channel_id, channel_id: scope.channel_id, name: scope.name, provider: "",
        environment: "production", currency: scope.currency, direction: scope.direction, quote: rate?.override ?? rate?.default ?? null };
    }) : (data.merchant_channel_quotes ?? []).filter(quote => !selectedSiteID || quote.site_id === selectedSiteID);
    const mockCatalog = <section className="workspace-panel platform-control-panel">
        <div className="heading panel-heading"><h2>{t(catalogLabel)}</h2>
          <div className="header-actions">
            {!merchant && <button className="secondary" onClick={() => { refresh(); catalogRates.refresh(); }}>{t("Refresh")}</button>}
            {!productionOperations && canWrite && !merchant && <button className="primary" onClick={() => setAction({ type: "channel" })}>{t("Configure mock channel")}</button>}
          </div>
        </div>
        {!productionOperations && !merchant && <p className="muted panel-note">{t("Sandbox channels use simulated providers. Set upstream cost and default merchant rates here.")}</p>}
        {!merchant && Boolean(catalogRates.error) && <div className="workspace-feedback"><p role="alert">{t("Could not load {resource}.", { resource: t("Channel pricing") })}</p>
          <button className="secondary" type="button" onClick={catalogRates.refresh}>{t("Retry")}</button></div>}
        <PlatformChannelCards channels={data.channels} label={t(catalogLabel)}
          pricingSummary={channel => <>
            {channel.limits?.map(limit => <p className="muted small" key={limit.direction}>
              {t(limit.direction === "collection" ? "Collection" : "Payout")} · {channel.currency} · {locale === "zh-CN" ? "上游单笔范围" : "Upstream per-order range"}{" "}
              {limit.min === null ? "—" : exactMoney(String(limit.min), channel.currency, productionOperations)} – {limit.max === null ? "—" : exactMoney(String(limit.max), channel.currency, productionOperations)}
            </p>)}
            {productionOperations && !merchant && (channel.directions?.includes("collection")) && (cycles.data ? <ChannelCycle channel={channel.id} currency={channel.currency} market={channel.market} editable={canWrite} cycle={cycles.data.find(c=>c.channel_id===channel.id && c.currency===channel.currency)} refresh={cycles.refresh}/> : <p>{cycles.error?t("Could not load {resource}.",{resource:t("Settlement cycle")}):t("Loading")}</p>)}
            {!merchant && availableRates && <CatalogRateSummary channel={channel} rates={availableRates} operator={canWrite} />}
          </>}
          onPricing={channel => setPricing({ channel })} onToggle={!productionOperations && canWrite && !merchant ? channel => setAction({ type: "status", channel }) : undefined} />
      </section>;

    return <>
      {merchant && <MerchantChannelRates quotes={merchantQuotes}
          sites={shownSites} open={quote => {
            const channel = data.channels.find(item => item.id === quote.channel_id && (!productionOperations || (item.currency === quote.currency && item.directions?.includes(quote.direction))));
            if (channel) setPricing({ channel, direction: quote.direction });
          }} />}
      {merchant && <section className="workspace-panel platform-control-panel">
        <div className="heading panel-heading">
          <div><h2>{merchantName ?? merchant}</h2><p className="muted small wrap">{merchant}</p></div>
          <button className="secondary" onClick={refresh}>{t("Refresh")}</button>
        </div>
        <div className="platform-setup">
          {productionOperations ? <ProductionAdministrator merchant={merchant} admin={data.administrator} operator={canWrite} refresh={refresh} />
            : data.onboarding ? <MerchantAdminInvitation session={session} invitation={data.onboarding} refresh={refresh} />
            : <div><h3>{t("Merchant workspace activation")}</h3><p>{t("No administrator invitation record")}</p></div>}
          {!productionOperations && <div><h3>{t("Collection setup")}</h3>
            <SiteCards sites={shownSites} focused={routeID ? undefined : selectedSiteID} />
            {selectedSiteID && <button className="text-button" onClick={() => { setScopeCleared(true); close(); }}>{t("Show all merchant sites")}</button>}
          </div>}
        </div>
        {!productionOperations && canWrite && <div className="overview-footer">
          <div className="header-actions">
            {assignmentSites.length > 0 && <button className="primary" onClick={() => setAction({ type: "assign" })}>{t("Assign channel")}</button>}
            {routeOptions.length > 0 && <button className="secondary" onClick={() => setAction({ type: "propose" })}>{t("Configure route")}</button>}
          </div>
          {!availableChannels.length && <p className="muted small">{t("Enable a mock channel before assigning it to a site.")}</p>}
          {proposed && canApprove(proposed) && <button className="primary" onClick={() => setAction({ type: "approve", route: proposed })}>
            {t("Activate pending route")}
          </button>}
        </div>}
      </section>}
      {!merchant && !catalogOnly && productionOperations && <p className="muted">{t("Select a merchant to view channel assignments, rates and API keys.")}</p>}
      {!merchant && !catalogOnly && !productionOperations && <p className="muted">{t(canWrite
        ? "Select a merchant to manage site assignments, routing and API keys."
        : "Select a merchant to view site assignments, routing and API keys.")}</p>}
      {!productionOperations && merchant && canWrite && <QuickChannelSwitch key={"switch:" + (selectedSiteID ?? "all")}
        sites={shownSites} initialSite={selectedSiteID} channels={switchChannels}
        preview={(site, channel) => setAction({ type: "switch", site, channel })}
        current={effective.flatMap(route => {
          const account = accountFor(route);
          return account ? [{ siteID: route.site_id, scope: applicabilityKey(route), channel: channelTitle(account, t) }] : [];
        })} />}
      {merchant && productionOperations && <ProductionRouting key={merchant} merchant={merchant} merchantName={merchantName} channels={data.channels}
        scopes={data.routing ?? []} focusRoute={focusRoute} openFunding={openFunding} rates={availableRates ?? []} operator={canWrite} refresh={refresh}
        price={(channel, direction) => setPricing({ channel, direction })} />}
      {merchant && <>
        {!productionOperations && <>
        <section className="workspace-panel platform-control-panel">
          <div className="heading panel-heading"><h2>{t("Site assignments")}</h2>
          </div>
          <WorkspaceTable label={t("Site assignments")} columns={[t("Site"), t("Account"), t("Applicability"), t("Status")]}>
            {accounts.filter(account => !selectedSiteID || account.site_id === selectedSiteID).map(account => <tr key={account.id}>
              <td>{siteName(account.site_id)}</td>
              <td>{channelSummary(account, true)}
                {data.channels.some(channel => channel.id === account.platform_channel_id) &&
                  <button className="text-button" type="button" onClick={() => {
                    const channel = data.channels.find(item => item.id === account.platform_channel_id);
                    if (channel) setPricing({ channel });
                  }}>
                    {t("Channel pricing")}
                  </button>}</td>
              <td>{profile(channelProfiles[account.provider])}</td>
              <td>{t(collectionAccountStatus(account, effective.some(route => route.account_id === account.id)))}</td>
            </tr>)}
          </WorkspaceTable>
        </section>
        <section className="workspace-panel platform-control-panel platform-routing-panel">
          <div className="heading panel-heading"><h2>{t("Collection routing")}</h2>
          </div>
          <WorkspaceTable label={t("Collection routing")} columns={[t("Site"), t("Route"), t("Status"), t("Submitted by"), t("Activated by"), ...(canWrite ? [t("Action")] : [])]}>
            {routes.filter(route => !selectedSiteID || route.site_id === selectedSiteID).map(route => {
              const account = accountFor(route);
              const status = routeStatus(route);
              const isPending = status === "Awaiting approval";
              const isUnavailable = !account?.enabled && (isPending || status === "In effect");
              return <tr key={route.id} ref={route.id === routeID ? routeRow : undefined}
                tabIndex={route.id === routeID ? -1 : undefined} className={route.id === routeID ? "target-route" : undefined}>
                <td>{siteName(route.site_id)}</td><td>{account ? channelSummary(account, true) : profile(route)}</td>
                <td>{t(isUnavailable ? (isPending ? "Proposed channel unavailable" : "Disabled") : status)}</td>
                <td>{route.submitted_by}</td><td>{route.approved_by ?? "—"}</td>
                {canWrite && <td>{isPending && <div className="header-actions">
                  {canApprove(route) && <button className="primary" onClick={() => setAction({ type: "approve", route })}>{t("Activate pending route")}</button>}
                  <button className="secondary" onClick={() => setAction({ type: "cancel", route })}>{t("Cancel proposal")}</button>
                </div>}</td>}
              </tr>;
            })}
          </WorkspaceTable>
        </section>
        </>}
        <section className="workspace-panel platform-control-panel">
          <div className="heading panel-heading"><h2>{t("API keys")}</h2>
            {canWrite && <button className="primary" onClick={() => setAction({ type: "issue" })}>{t("Create key")}</button>}
          </div>
          <WorkspaceTable label={t("API keys")} columns={[t("Key"), t("Status"), t("Created by"), t("Retired by"), ...(canWrite ? [t("Action")] : [])]}>
            {data.keys?.map(key => <tr key={key.id}>
              <td>{t(key.label)}<span className="resource-id account-id muted">{key.id}</span></td><td>{t(key.revoked_at ? "Retired" : "Active")}</td>
              <td>{key.created_by ?? "—"}</td><td>{key.revoked_by ?? "—"}</td>
              {canWrite && <td>{key.platform && !key.revoked_at && <button className="secondary danger"
                onClick={() => setAction({ type: "revoke", key })}>{t("Revoke key")}</button>}</td>}
            </tr>)}
          </WorkspaceTable>
        </section>
      </>}
      {merchant && <PlatformOrderFees key={"fees:" + (selectedSiteID ?? "all")} token={session.token} merchant={merchant} siteID={selectedSiteID} operator={canWrite} />}
      {pricing && <div ref={pricingPanel}><ChannelPricePanel key={[pricing.channel.id, pricing.channel.currency, pricing.channel.method, pricing.direction ?? "all"].join(":")}
        channel={pricing.channel} selectedDirection={pricing.direction} token={session.token}
        merchant={merchant} operator={canWrite} close={() => setPricing(undefined)}
        onPublished={() => { if (merchant) refresh(); else catalogRates.refresh(); }} /></div>}
      {merchant && !productionOperations ? <details className="platform-advanced-rates">
        <summary>{t("Set rates for channels pending activation")}</summary>{mockCatalog}
      </details> : !merchant ? mockCatalog : null}
      {!merchant && Boolean(data.upstream_integrations?.length) && <section className="workspace-panel platform-control-panel"
        aria-label={t("Upstreams pending onboarding")}>
        <div className="heading panel-heading"><h2>{t("Upstreams pending onboarding")}</h2></div>
        <ul className="platform-card-grid platform-merchant-cards">
          {data.upstream_integrations?.map(channel => <li className="platform-card" key={channel.id}>
            <div className="platform-card-heading">
              <div className="platform-card-identity"><h3>{t(channel.name)}</h3>
                <span className="muted small">{channel.market} · {channel.currency}</span>
              </div>
              <span className="tag blue">{t("Pending verification")}</span>
            </div>
            <dl className="platform-card-meta">
              <div><dt>{t("Capabilities")}</dt><dd>{[channel.collections && t("Collection"), channel.payouts && t("Payout")].filter(Boolean).join(" · ")}</dd></div>
              <div><dt>{t("Connection test")}</dt><dd>{t(channel.validation_status === "signature_rejected" ? "Signature verification failed" : "Pending verification")}</dd></div>
              <div><dt>{t("Routing")}</dt><dd>{t("Unavailable for routing")}</dd></div>
            </dl>
          </li>)}
        </ul>
      </section>}
      {!catalogOnly && <PlatformAudit token={session.token} audit={data.audit} platformOwner={session.platformOwner} />}
      {dialog()}
    </>;
  }}</LoadState>;
}

type AuditPage = { events: { entry: string; occurred_at: string; operator_id?: string; operator_email?: string; database_actor?: string; action: string; resource_id: string }[]; next?: string };

function PlatformAudit({ token, audit, platformOwner }: { token: string; audit: AuditEntry[]; platformOwner?: boolean }) {
  const { t, dateTime } = useLocale();
  const [pages, setPages] = useState([""]);
  const [operator, setOperator] = useState("");
  const team = useWorkspaceData<PlatformTeamMember[]>(productionOperations && platformOwner ? platformTeamPath : undefined, token);
  const cursor = pages[pages.length - 1];
  const read = useCallback((path: string, key?: string, init?: RequestInit) => request<AuditPage>(path, key, init), [audit]);
  const state = useWorkspaceData(productionOperations ? base + "/audit?" + new URLSearchParams({ before: cursor, operator_id: operator }) : undefined,
    token, read);
  function page(next: string[]) {
    state.reload();
    setPages(next);
  }
  function table(items: AuditEntry[]) {
    return <WorkspaceTable label={t("Platform audit")} columns={[t("Created"), t("Operator"), t("Action"), t("Merchant ID"), t("Resource")]}>
          {items.map(item => <tr key={item.id}>
            <td>{dateTime(item.occurred_at)}</td><td>{item.actor}</td><td>{t(actions[item.action as keyof typeof actions] ?? item.action)}</td>
            <td>{item.merchant_id ?? "—"}</td>
            <td className="wrap">{item.provider ? <>
              {channelTitle({ ...item, id: item.account_id ?? item.resource_id, provider: item.provider }, t)}
              <ChannelTechnical value={{ ...item, id: item.account_id ?? item.resource_id, provider: item.provider }}
                account={Boolean(item.account_id)} resource={item.account_id === item.resource_id ? undefined : item.resource_id} />
            </> : <details className="channel-technical"><summary>{t("Technical details")}</summary><code>{item.resource_id}</code></details>}</td>
          </tr>)}
        </WorkspaceTable>;
  }
  return <section className="workspace-panel platform-control-panel">
    <div className="heading panel-heading"><h2>{t("Platform audit")}</h2>
      {productionOperations && <button className="secondary" onClick={state.refresh}>{t("Refresh")}</button>}
    </div>
    {productionOperations && !platformOwner && <p className="muted">{t("Showing your operations only.")}</p>}
    {productionOperations && platformOwner && <label className="overview-filters">{t("Filter by operator")}
      <select value={operator} onChange={event => { state.reload(); setPages([""]); setOperator(event.target.value); }}>
        <option value="">{t("All operators")}</option>
        {team.data?.map(member => <option key={member.id} value={member.id}>{member.email}</option>)}
      </select>
    </label>}
    {productionOperations ? <LoadState state={state} label={t("Platform audit")}>{data => <>
      {table(data.events.map(event => ({ ...event, id: event.entry, actor: event.operator_email ?? event.operator_id ?? event.database_actor ?? "—",
        merchant_id: /^M[0-9]{5,12}(?::|$)/.test(event.resource_id) ? event.resource_id.split(":")[0] : undefined })))}
      {pages.length > 1 && <button className="secondary" onClick={() => page(pages.slice(0, -1))}>{t("Previous page")}</button>}
      {data.next && <button className="secondary" onClick={() => page([...pages, data.next!])}>{t("Next page")}</button>}
    </>}</LoadState> : table(audit)}
  </section>;
}
