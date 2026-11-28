import { PayoutFilterSelect } from "./PayoutFilterSelect";
import { PlatformPayoutOrders, initialPayoutFilters } from "./PlatformPayoutOrders";
import { PlatformCollectionOrders } from "./PlatformCollectionOrders";
import { OrderEvidence } from "./OrderEvidence";
import { useEffect, useLayoutEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ArrowLeft, Buildings, CreditCard, GearSix, House, Moon, Plugs, SidebarSimple, Stack, ChartLineUp, Users } from "@phosphor-icons/react";
import { ApiError, request } from "./api";
import type { Provider } from "./ChannelIdentity";
import { PaymentStatusBadge } from "./CollectionEvents";
import { Dialog } from "./Dialog";
import { languages, LanguageSelect, resolveLocale, useLocale } from "./locale";
import { Details, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

import { SiteCards, SiteExplanation, type SiteCard } from "./SiteCards";
import { ChannelMarketPreviews, PlatformChannelCards } from "./PlatformChannelCards";
import { MerchantOnboarding, PlatformMerchantCards, PlatformMerchants, type PlatformMerchant } from "./PlatformMerchants";
import { OrderRiskOverview } from "./DispatchControls";
import { PlatformFinance } from "./PlatformFinance";
import type { RouteFocus } from "./ProductionPlatformControls";
import { PlatformOperations } from "./PlatformOperations";
import { PlatformTeam } from "./PlatformTeam";

import { platformAPI as api, platformEnvironment, productionOperations } from "./platformEnvironment";
import { ProductionPlatformAccess } from "./ProductionPlatformAccess";

const sidebarKey = "flowxpay.platform.sidebar.collapsed";
const payoutThemeKey = "flowxpay.platform.payouts.theme";

const navigation = [
  ["Overview", House], ["Upstream channels", Stack], ["Merchants", Buildings], ["Payments", CreditCard], ["Payouts", CreditCard], ["Platform finance", ChartLineUp], ["Channel accounts", Plugs], ["Operations", GearSix], ["Platform team", Users],
] as const;
const operationsNav: {zh:string;en:string;pages:Page[]}[] = [
  {zh:"商户与通道",en:"Merchants and channels",pages:["Merchants","Upstream channels","Channel accounts","Operations"]},
  {zh:"订单",en:"Orders",pages:["Payments","Payouts"]},
  {zh:"资金",en:"Funds",pages:["Platform finance"]},
];
type OrderTarget = { direction: "collection" | "payout"; reference?: string; currency?: string; pending?: boolean };
type Page = typeof navigation[number][0];
export interface PlatformSession { token: string; user: string; role: "viewer" | "operator"; platformOwner?: boolean }
export type PlatformOrder = { id: string; merchant: string; reference: string; currency: string; amount_minor: number | string;
  status: string; created_at: string; checkout_needs_review?: boolean;
  market?: string; method?: string; channel_id?: string; account_id?: string; updated_at?: string;
  upstream_activity?: { attempted_at?: string; submitted_at?: string; lookup_at?: string; lookup_result?: string;
    lookup_attempts: number; next_lookup_at?: string; final_at?: string; conflict_at?: string; callback_state?: string };
};

interface PlatformData {
  merchants: PlatformMerchant[];
  volumes: { currency: string; orders: number; paid: number; volume_minor: string }[];
  payments: PlatformOrder[];
  channels: {
    id: string; merchant: string; merchant_name: string; site_name?: string; currency?: string; environment?: string;
    provider: Provider; enabled: boolean; direction?: RouteFocus["direction"]; permitted?: boolean; platform_channel_id?: string; platform_channel_name?: string;
  }[];
  sites?: SiteCard[];
  tasks?: { kind: TaskKind; total: number; items: PlatformTask[] }[];
}
const taskNames = {
  assignment: productionOperations ? "Merchants awaiting channel assignment" : "Sites awaiting channel assignment",
  can_approve: "Routes ready to activate",
  pending: "Routes awaiting approval",
  blocked: productionOperations ? "Merchants awaiting collection readiness" : "Proposed channels unavailable",
} as const;
type TaskKind = keyof typeof taskNames;
type SiteTarget = { site_id: string; kind?: never } | PlatformTask | { route: RouteFocus; site_id?: never; kind?: never };
export interface PlatformTask {
  kind: TaskKind;
  merchant_id: string; merchant_name: string; site_id: string; site_name: string;
  route_id?: string; currency?: string; method?: string;
}

export function PlatformWorkspace({ logo }: { logo: ReactNode }) {
  const { t, localizedLink } = useLocale();
  const [session, setSession] = useState<PlatformSession>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      setSession(await request<PlatformSession>(api + "/login", undefined, {
        method: "POST", body: JSON.stringify({ user: form.get("user"), password: form.get("password") }),
      }));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to sign in.");
    } finally {
      setBusy(false);
    }
  }

  if (session) return <PlatformConsole logo={logo} session={session} signOut={notice => {
    setSession(undefined);
    setError(notice ?? "");
  }} />;

  if (productionOperations) return <ProductionPlatformAccess logo={logo} onSession={setSession} notice={error} />;

  return <main className="connect-shell">
    <section className="connect">
      <div className="access-header">{logo}<LanguageSelect /></div>
      <span className="tag blue">{t(platformEnvironment)}</span>
      <h1>{t("Platform operations")}</h1>
      <p className="muted access-description">{t("Sign in with a platform account to view merchants and transactions.")}</p>
      <form onSubmit={login}>
        <label htmlFor="platform-user">{t("Platform account")}</label>
        <input id="platform-user" name="user" autoComplete="username" required maxLength={254} />
        <label htmlFor="platform-password">{t("Password")}</label>
        <input id="platform-password" name="password" type="password" autoComplete="current-password" required maxLength={72} />
        {error && <p className="error" role="alert">{t(error)}</p>}
        <button className="primary full" disabled={busy}>{t(busy ? "Please wait…" : "Sign in")}</button>
      </form>
      <div className="access-links"><a className="text-button" href={localizedLink("/")}>{t("Merchant workspace")}</a></div>
    </section>
  </main>;
}

export function PlatformConsole({ logo, session, signOut, demoLinks, initialPage = "Overview" }: {
  logo: ReactNode; session: PlatformSession; signOut: (notice?: string) => void; initialPage?: Page; demoLinks?: ReactNode;
}) {
  const { t, locale, setLocale } = useLocale();
  const [orderTarget, setOrderTarget] = useState<OrderTarget>();
  const [focusDifferences, setFocusDifferences] = useState(false);
  const [focusBindings, setFocusBindings] = useState(false);
  const [page, setPage] = useState<Page>(initialPage);
  const [darkPayouts, setDarkPayouts] = useState(() => {
    const theme = new URLSearchParams(location.search).get("theme");
    if (theme === "light" || theme === "dark") return theme === "dark";
    try { return localStorage.getItem(payoutThemeKey) === "dark"; }
    catch { return false; }
  });

  function togglePayoutTheme() {
    const next = !darkPayouts;
    setDarkPayouts(next);
    const url = new URL(location.href);
    if (url.searchParams.has("theme")) {
      url.searchParams.delete("theme");
      history.replaceState(history.state, "", url);
    }
    try { localStorage.setItem(payoutThemeKey, next ? "dark" : "light"); }
    catch { /* The selection still applies when browser storage is unavailable. */ }
  }

  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem(sidebarKey) === "true"; }
    catch { return false; }
  });
  const sidebarLabel = sidebarCollapsed ? t("Expand navigation") : t("Collapse navigation");

  function toggleSidebar() {
    const next = !sidebarCollapsed;
    setSidebarCollapsed(next);
    try { localStorage.setItem(sidebarKey, String(next)); }
    catch { /* Keep the current selection in this session. */ }
  }

  const [merchant, setMerchant] = useState("");
  const [target, setTarget] = useState<SiteTarget>();
  const [signingOut, setSigningOut] = useState(false);
  const [logoutError, setLogoutError] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const focusConfiguration = useRef(false);
  useLayoutEffect(() => {
    if (page === "Operations" && focusConfiguration.current) {
      heading.current?.focus();
      focusConfiguration.current = false;
    }
  }, [page]);

  function configure(id: string, next?: SiteTarget) {
    focusConfiguration.current = page !== "Operations";
    setTarget(next);
    setMerchant(id);
    setPage("Operations");
  }

  async function logout() {
    setSigningOut(true);
    setLogoutError(false);
    try {
      await request<void>(api + "/logout", session.token, { method: "POST", ...(productionOperations ? { body: "{}" } : {}) });
      signOut();
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 401) signOut();
      else if (productionOperations) setLogoutError(true);
      else signOut("The platform session could not be closed.");
    } finally {
      setSigningOut(false);
    }
  }

  function navButton(name: Page) {
    const Icon = navigation.find(([id]) => id === name)![1];
    return <button key={name} className={name === page ? "active" : ""}
      aria-current={name === page ? "page" : undefined} onClick={() => {
        setTarget(undefined); setOrderTarget(undefined); setFocusBindings(false); setFocusDifferences(false); setPage(name);
      }}>
      <Icon size={21} aria-hidden="true" />{t(productionOperations && name === "Channel accounts" ? "Merchant channel assignments" : name)}
    </button>;
  }

  return <div className="app-shell member-shell platform-shell" data-sidebar-collapsed={sidebarCollapsed} data-payout-theme={page === "Payouts" ? (darkPayouts ? "dark" : "light") : undefined}>
    <div className="platform-brand">
      {logo}
      <button type="button" className="sidebar-toggle" title={sidebarLabel} aria-label={sidebarLabel}
        aria-controls="platform-navigation" aria-expanded={!sidebarCollapsed} onClick={toggleSidebar}>
        <SidebarSimple size={20} aria-hidden="true" />
      </button>
    </div>
    <header className="topbar">
      {page === "Payouts" && demoLinks ? demoLinks : <div className="identity"><span className="muted small">{t("Platform operations")}</span><strong className="wrap">{session.user}</strong></div>}
      <span className="tag blue">{page === "Payouts" && demoLinks ? (locale === "zh-CN" ? "只读演示" : "Read-only demo") : t(platformEnvironment)}</span>
      <button className="secondary disconnect" onClick={() => void logout()} disabled={signingOut} aria-label={t("Sign out")}>
        <ArrowLeft size={16} /><span>{t("Sign out")}</span>
      </button>
      {page === "Payouts" ? <PayoutFilterSelect id="flowxpay-language" label={t("Language")}
        options={Object.entries(languages).map(([value, label]) => ({ value, label }))}
        value={locale} onChange={value => setLocale(resolveLocale(value))} /> : <LanguageSelect />}
    </header>
    <nav id="platform-navigation" className="nav" hidden={sidebarCollapsed} aria-label={t("Platform operations")}>
      {productionOperations ? <>
        {navButton("Overview")}
        {operationsNav.map(group=><div className="nav-group" key={group.en}>
          <p>{locale === "zh-CN" ? group.zh : group.en}</p>
          {group.pages.map(navButton)}
        </div>)}
        {session.platformOwner && navButton("Platform team")}
      </> : navigation.filter(([name]) => name !== "Platform team").map(([name])=>navButton(name))}
    </nav>
    <main className="workspace">
      {logoutError && <p className="error" role="alert">{t("The platform session could not be closed.")}</p>}
      <div className="heading page-heading">
        <h1 ref={heading} tabIndex={-1}>{t(page === "Payouts" ? "Payout orders" : productionOperations && page === "Channel accounts" ? "Merchant channel assignments" : page)}</h1>
        {page === "Payouts" && <button type="button" role="switch" aria-checked={darkPayouts}
          className="secondary payout-theme-switch" onClick={togglePayoutTheme}>
          <Moon size={17} aria-hidden="true" />{locale === "zh-CN" ? "深色模式" : "Dark mode"}
          <span className="payout-theme-track" aria-hidden="true" />
        </button>}
      </div>
      {page === "Platform team" && session.platformOwner
        ? <PlatformTeam token={session.token} />
        : page === "Merchants"
        ? <PlatformMerchants session={session} signOut={signOut} configure={configure} />
        : page === "Upstream channels"
          ? <>
            <PlatformOperations session={session} merchant="" catalogOnly />
            <ChannelMarketPreviews />
          </>
          : <PlatformReports key={page === "Payouts" ? page : page + ":" + merchant} session={session} page={page} merchant={merchant}
          selectMerchant={(id, differences = false) => { setTarget(undefined); setFocusDifferences(differences); setMerchant(id); }} configure={configure}
          orderTarget={orderTarget} openOrders={next => {
            setOrderTarget(next);
            setMerchant("");
            setPage(next.direction === "payout" ? "Payouts" : "Payments");
          }} target={target} signOut={signOut} focusBindings={focusBindings} openFinance={(bindings = false, differences = false) => { setFocusBindings(bindings); setFocusDifferences(differences); if (differences) setMerchant(""); setPage("Platform finance"); }} focusDifferences={focusDifferences} />}
    </main>
  </div>;
}

function PlatformReports({ session, merchant, page, selectMerchant, configure, target, signOut, openFinance, focusBindings, focusDifferences, orderTarget, openOrders }: {
  session: PlatformSession; merchant: string; page: Page; selectMerchant: (id: string, differences?: boolean) => void;
  configure: (id: string, target?: SiteTarget) => void; target?: SiteTarget; signOut: (notice?: string) => void; openFinance: (bindings?: boolean, differences?: boolean) => void; focusBindings: boolean; focusDifferences: boolean; orderTarget?: OrderTarget; openOrders: (target: OrderTarget) => void;
}) {
  const { t, exactMoney, money, dateTime, locale } = useLocale();
  const [creating, setCreating] = useState(false);
  const [order, setOrder] = useState<PlatformOrder>();
  const [evidenceOrder, setEvidenceOrder] = useState("");
  const [payoutFilters, setPayoutFilters] = useState(initialPayoutFilters);
  const [collectionRevision, setCollectionRevision] = useState(0);
  const [paymentSearch, setPaymentSearch] = useState(orderTarget?.reference ?? "");
  const [paymentReference, setPaymentReference] = useState(orderTarget?.reference ?? "");
  const [riskFilter, setRiskFilter] = useState(orderTarget?.pending ? orderTarget.currency : undefined);
  const taskGroups = useRef<Partial<Record<TaskKind, HTMLElement | null>>>({});
  const visibleTasks: TaskKind[] = productionOperations ? ["assignment", "blocked"] : session.role === "operator"
    ? ["assignment", "can_approve", "blocked"] : ["assignment", "pending", "blocked"];
  const filters = new URLSearchParams({ merchant });
  if (page === "Overview") filters.set("tasks", "1");
  if (page === "Payouts") filters.set("direction", "payout");
  if ((page === "Payouts" || (page === "Payments" && !productionOperations)) && paymentReference) filters.set("reference", paymentReference);
  if (page === "Payouts" && riskFilter) {
    filters.set("currency", riskFilter);
    filters.set("pending", "1");
  }
  const state = useWorkspaceData<PlatformData>(api + "/dashboard?" + filters, session.token);
  useEffect(() => {
    if (state.error instanceof ApiError && state.error.status === 401) signOut(productionOperations ? "Sign in again to continue." : "An active platform preview session is required.");
  }, [state.error, signOut]);

  const orderAmount = (item: PlatformOrder) => typeof item.amount_minor === "string"
    ? exactMoney(item.amount_minor, item.currency, productionOperations) : money(item.amount_minor, item.currency);

  return <><LoadState state={state} label={t("Platform operations")}>{data => <div className="merchant-overview">
    {page !== "Payouts" && <section className="workspace-panel">
      <div className="heading panel-heading">
        <label className="overview-filters">{t("Merchant")} <select id="ops-merchant-scope" value={merchant} onChange={event => selectMerchant(event.target.value)}>
          <option value="">{t("All merchants")}</option>
          {data.merchants.map(item => <option key={item.id} value={item.id}>
            {item.name}{data.merchants.some(other => other.id !== item.id && other.name === item.name) ? " · " + item.id : ""}
          </option>)}
        </select></label>
        <div className="header-actions">
          <span className="muted small">{t(session.role === "operator" ? "Platform operator" : productionOperations ? "Read-only operator" : "Read-only preview")}</span>
          {page === "Overview" && session.role === "operator" && <button className="primary" onClick={() => setCreating(true)}>
            {t("Open merchant account")}
          </button>}
        </div>
      </div>
    </section>}
    {page === "Overview" && productionOperations && <OrderRiskOverview token={session.token}
      openPending={currency => openOrders({direction:"payout", currency, pending:true})}
      openConflict={(direction, reference) => openOrders({direction, reference})}
      openReconciliation={() => openFinance(false, true)} />}
    {page === "Overview" && data.tasks && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Collection setup tasks")}</h2></div>
      <div className="platform-card-grid platform-merchant-cards">
        {visibleTasks.map(kind => {
          const total = data.tasks?.find(group => group.kind === kind)?.total ?? 0;
          return <button className="platform-card task-summary-card" key={kind} disabled={!total}
            onClick={() => taskGroups.current[kind]?.focus()}>
            <span>{t(taskNames[kind])}</span><strong>{total}</strong>
          </button>;
        })}
      </div>
      {data.tasks.length ? <div className="platform-task-groups">
        {visibleTasks.map(kind => {
          const group = data.tasks?.find(item => item.kind === kind);
          if (!group) return null;
          return <section className="platform-task-group" key={kind} tabIndex={-1}
            ref={node => { taskGroups.current[kind] = node; }}>
            <h3>{t(taskNames[kind])}</h3>
            <ul>{group.items.map(item => <li key={item.route_id ?? item.site_id ?? item.merchant_id}>
              <button className="text-button wrap" onClick={() => configure(item.merchant_id, item)}>
                {item.merchant_name}{item.site_name && " · " + item.site_name}{item.currency && item.method ? " · " + item.currency + " · " + t(item.method) : ""}
              </button>
            </li>)}</ul>
            {group.total > group.items.length && <p className="muted small">{t("Showing {count} of {total}. Filter by merchant to narrow the list.", {
              count: String(group.items.length), total: String(group.total),
            })}</p>}
          </section>;
        })}
      </div> : <p className="muted empty">{t("No collection setup tasks in this scope.")}</p>}
    </section>}
    {page === "Overview" && <section className="workspace-panel overview-summary">
      <div className="heading panel-heading"><h2>{t("Successful collection volume")}</h2><div className="header-actions">
        <span className="muted small">{t("Last 7 days")} · UTC</span>
        <button type="button" className="secondary" onClick={() => openFinance()}>{t("Platform finance")}</button>
      </div></div>
      {data.volumes.length ? <div className="overview-metrics platform-metrics">
        {data.volumes.map(v => <div key={v.currency}>
          <span>{v.currency}</span><strong>{exactMoney(v.volume_minor, v.currency, productionOperations)}</strong>
          <small>{t("Paid orders / total orders")} · {v.paid} / {v.orders}</small>
        </div>)}
      </div> : <p className="empty muted">{t("No payments match your search.")}</p>}
    </section>}
    {page === "Overview" && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Merchants")}</h2></div>
      <PlatformMerchantCards merchants={data.merchants} configure={configure} />
    </section>}
    {page === "Overview" && data.sites && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Merchant sites")}</h2></div>
      <SiteExplanation />
      <SiteCards sites={data.sites} platform action={session.role === "operator" ? "Configure site channels" : "View site channels"}
        open={site => configure(site.merchant_id, { site_id: site.id })} />
    </section>}
    {page === "Payments" && productionOperations && <PlatformCollectionOrders
      key={merchant + "/" + (orderTarget?.reference ?? "")} merchant={merchant} initialReference={orderTarget?.reference}
      token={session.token} revision={collectionRevision} openOrder={setOrder} openEvidence={setEvidenceOrder} />}
    {page === "Payouts" && <PlatformPayoutOrders orders={data.payments} merchants={data.merchants}
      merchant={merchant} onMerchant={id => { state.reload(); selectMerchant(id); }}
      search={paymentSearch} onSearchChange={setPaymentSearch} reference={paymentReference}
      applySearch={next => { if (next !== paymentReference) { state.reload(); setPaymentReference(next); } }}
      riskFilter={riskFilter} clearRisk={() => { state.reload(); setRiskFilter(undefined); }}
      filters={payoutFilters} setFilters={setPayoutFilters} openOrder={setOrder} />}
    {page === "Payments" && !productionOperations && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Recent transactions")}</h2>
        <form className="overview-filters payment-reference-filter" onSubmit={event => {
          event.preventDefault();
          const next = paymentSearch.trim();
          if (next !== paymentReference) {
            state.reload();
            setPaymentReference(next);
          }
        }}>
          <input id="ops-payment-reference" aria-label={t("Search payments")} placeholder={t("Order reference or ID")}
            value={paymentSearch} onChange={event => setPaymentSearch(event.target.value)} maxLength={80}
            pattern="[a-zA-Z0-9._:\-]{1,80}" />
          <button className="secondary">{t("Search")}</button>
          {paymentReference && <button className="secondary" type="button" onClick={() => {
            state.reload();
            setPaymentSearch("");
            setPaymentReference("");
          }}>{t("Clear")}</button>}
        </form>
      </div>
      {data.payments.length ? <WorkspaceTable label={t("Payment list")} columns={[t("Merchant ID"), t("Platform reference"), t("Merchant reference"), t("Amount"), t("Status"), t("Created")]}>
        {data.payments.map(item => <tr key={item.id}>
          <td>{item.merchant}</td><td className="resource-id order-number"><button className="text-button wrap" onClick={() => setOrder(item)}>{item.id}</button></td>
          <td>{item.reference}</td><td>{orderAmount(item)}</td>
          <td><PaymentStatusBadge payment={item} /></td><td>{dateTime(item.created_at)}</td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("No payments match your search.")}</p>}
    </section>}
    {page === "Platform finance" && <PlatformFinance session={session} merchant={merchant} focusBindings={focusBindings} focusDifferences={focusDifferences} selectMerchant={selectMerchant} openOrder={setEvidenceOrder} />}
    {page === "Operations" && <PlatformOperations key={merchant} session={session} merchant={merchant} merchantName={data.merchants.find(item => item.id === merchant)?.name}
      task={target?.kind ? target : undefined} siteID={target?.site_id} focusRoute={target && "route" in target ? target.route : undefined} openFunding={() => openFinance(true)} />}
    {page === "Channel accounts" && <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t(productionOperations ? "Merchant channel assignments" : "Channel accounts")}</h2></div>
      {productionOperations && <p className="muted panel-note">{locale === "zh-CN"
        ? "查看上游分配到哪些商户，以及代收和代付能否接单。修改分配、费率和权限请在该商户的运营设置操作。"
        : "Review merchant assignments and readiness by direction. To change assignments, rates or access, open the merchant’s operations settings."}</p>}
      <PlatformChannelCards channels={data.channels} label={t(productionOperations ? "Merchant channel assignments" : "Channel accounts")} accounts
        accountAction={t(session.role === "operator" ? "Manage assignment" : "View assignment")}
        onOpenAccount={productionOperations ? account => {
          const {platform_channel_id, direction, currency} = account;
          if (platform_channel_id && direction && currency) configure(account.merchant, {route:{channel_id:platform_channel_id,direction,currency}});
          else configure(account.merchant);
        } : undefined} />
    </section>}
  </div>}</LoadState>
    {order && <Dialog drawer={page !== "Payouts"} className={page === "Payouts" ? "payout-receipt" : undefined} title={t(page === "Payouts" ? "Payout details" : "Payment details")} close={() => setOrder(undefined)}>
      {page === "Payouts" && <div className="payout-receipt-total">
        <div><span>{state.data?.merchants.find(merchant => merchant.id === order.merchant)?.name ?? order.merchant}</span><strong>{orderAmount(order)}</strong></div>
        <PaymentStatusBadge payment={order} payout />
      </div>}
      {page === "Payouts" && order.updated_at && <p className="payout-receipt-updated">{t("Updated")} · {dateTime(order.updated_at)}</p>}
      <Details fields={[
        [t("Merchant ID"), order.merchant], [t("Platform reference"), order.id], [t("Merchant reference"), order.reference],
        ...(page === "Payouts" ? [] : [
          [t("Amount"), orderAmount(order)], [t("Status"), <PaymentStatusBadge payment={order} />],
        ] as [string, ReactNode][]),
        [t("Applicability"), [order.market, order.currency, t(order.method ?? "")].filter(Boolean).join(" · ")],
        [t(order.account_id ? "Account ID" : "Channel ID"), order.channel_id ?? order.account_id ?? "—"], [t("Created"), dateTime(order.created_at)],
        ...(page === "Payouts" && order.updated_at ? [] : [[t("Updated"), order.updated_at ? dateTime(order.updated_at) : "—"] as [string, string]]),
      ]} />
      {order.checkout_needs_review && <p className="panel-note">{t("The payment code has expired. We are checking the original order.")}</p>}
      {productionOperations && <button className="secondary" type="button" onClick={() => { setEvidenceOrder(order.id); setOrder(undefined); }}>
        {locale === "zh-CN" ? "查看原单查证" : "View order evidence"}
      </button>}
      {productionOperations && page === "Payouts" && <PayoutActivity order={order} />}
    </Dialog>}
    {evidenceOrder && <OrderEvidence key={evidenceOrder} id={evidenceOrder} token={session.token} operator={session.role === "operator"} close={() => { setEvidenceOrder(""); setCollectionRevision(value => value + 1); state.refresh(); }} />}
    {creating && page === "Overview" && session.role === "operator" && <MerchantOnboarding session={session}
      close={() => setCreating(false)} created={state.refresh} configure={configure} />}
  </>;
}

function PayoutActivity({ order }: { order: PlatformOrder }) {
  const { locale, dateTime } = useLocale();
  const zh = locale === "zh-CN";
  const upstream = order.upstream_activity;
  const events = [
    { label: zh ? "平台创建订单" : "Order created", at: order.created_at },
    { label: zh ? "开始向上游派单" : "Dispatch attempted", at: upstream?.attempted_at },
    { label: zh ? "收到上游提交响应" : "Upstream submission response recorded", at: upstream?.submitted_at },
    { label: zh ? "最近一次上游查单" : "Last upstream lookup", at: upstream?.lookup_at, value: upstream?.lookup_result },
    { label: zh ? "确认最终结果" : "Final result confirmed", at: upstream?.final_at, value: order.status },
    { label: zh ? "收到冲突回调" : "Conflicting callback received", at: upstream?.conflict_at, value: upstream?.callback_state },
  ].filter(event => event.at).sort((a,b)=>Date.parse(a.at!) - Date.parse(b.at!));
  return <section className="payout-activity">
    <h3>{zh ? "订单进度" : "Order activity"}</h3>
    <ol>{events.map(event => <li key={event.label}>
      <strong>{event.label}</strong>
      <span>{dateTime(event.at!)}{event.value && <> · {event.value}</>}</span>
    </li>)}</ol>
    {order.status === "pending_confirmation" && <p>{upstream?.next_lookup_at
      ? (zh ? "下次计划查单" : "Next scheduled lookup") + " · " + dateTime(upstream.next_lookup_at)
      : (zh ? "暂无下次查单时间" : "No next lookup time recorded")}</p>}
  </section>;
}
