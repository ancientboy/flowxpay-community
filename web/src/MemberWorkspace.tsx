import type { IntegrationProduct } from "./integration-product";
import { MerchantHomeTasks } from "./MerchantHomeTasks";
import {ProductionFunds} from "./ProductionFunds";
import {MerchantEvents} from "./MerchantEvents";
import type { WorkspaceCapabilities } from "./memberEnvironment";
import { ConsoleShell } from "./ConsoleShell";
import { useLocale } from "./locale";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { BookOpen, Buildings, PlayCircle, GearSix, Plugs, Bank, ClipboardText, Key, GitBranch, WarningCircle, House, CreditCard, ChartLineUp } from "@phosphor-icons/react";
import { PaymentDemos } from "./PaymentDemos";
import { MerchantDocs } from "./MerchantDocs";
import { Chargebacks } from "./Chargebacks";
import { Settlement } from "./Settlement";
import { APIKeys, CollectionRouting } from "./WorkspaceControls";
import { MerchantRates } from "./ChannelPricing";
import { Payouts } from "./Payouts";
import { Details, InventoryForm, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";
import { channelSubtitle, channelTitle, type Provider } from "./ChannelIdentity";
import { SiteCards, SiteExplanation, SiteFacts, siteStatus, type Site, type SiteCard } from "./SiteCards";
import { Dialog } from "./Dialog";
import { base, Invitations, message, post, TeamSecurity, type MemberSession } from "./WorkspaceAccess";

import { WorkspaceOverview, WorkspacePayments, type WorkspaceSetup } from "./WorkspaceOverview";

const pages = [
  { title: "Overview", icon: House },
  { title: "Business reports", icon: ChartLineUp },
  { title: "Start integration", icon: BookOpen },
  { title: "Payments", icon: CreditCard },
  { title: "Payment demos", icon: PlayCircle },
  { title: "API documentation", icon: BookOpen },
  { title: "Team & security", icon: GearSix },
  { title: "Sites", icon: Buildings },
  { title: "Channel accounts", icon: Plugs },
  { title: "API keys", icon: Key },
  { title: "Collection routing", icon: GitBranch },
  { title: "Chargebacks", icon: WarningCircle },
  { title: "Payouts", icon: Bank },
  { title: "Settlement", icon: ClipboardText },
] as const;

type MerchantPage = typeof pages[number]["title"];

const navGroups: { zh: string; en: string; pages: MerchantPage[] }[] = [
  { zh: "订单", en: "Orders", pages: ["Payments", "Payouts", "Chargebacks"] },
  { zh: "资金与通道", en: "Funds and channels", pages: ["Settlement", "Business reports", "Channel accounts"] },
  { zh: "接入", en: "Integration", pages: ["Start integration", "Payment demos", "API documentation", "API keys"] },
  { zh: "设置", en: "Settings", pages: ["Team & security"] },
];

const roles = { admin: "Merchant admin", operator: "Operator", viewer: "Viewer" };
export type { Site } from "./SiteCards";
export interface ChannelAccount {
  id: string; site_id: string; provider: Provider; environment: "sandbox";
  enabled: boolean; routing_standby: boolean; platform_channel_id?: string; platform_channel_name?: string;
}

export function MemberWorkspace({ logo, token, member, signedOut, endSession, demoLinks,
  environment = "Sandbox", capabilities }: {
  environment?: "Production" | "Sandbox";
  capabilities: WorkspaceCapabilities;
  endSession?: () => Promise<void>;
  demoLinks?: ReactNode;
  logo: ReactNode;
  token: string;
  member: MemberSession;
  signedOut: (notice?: string) => void;
}) {
  const { t, locale } = useLocale();
  const zh = locale === "zh-CN";
  const production = environment === "Production";
  const [page, setPage] = useState<MerchantPage>("Overview");
  const [channelTab, setChannelTab] = useState<"rates" | "history">("rates");
  const [statementAccount, setStatementAccount] = useState("");
  const [orderRef, setOrderRef] = useState("");
  const [pendingPayouts, setPendingPayouts] = useState(false);
  const mainHeading = useRef<HTMLHeadingElement>(null);
  const previousPage = useRef(page);
  useEffect(() => {
    if (previousPage.current !== page) {
      mainHeading.current?.focus();
      previousPage.current = page;
    }
  }, [page]);
  const [selectedSite, setSelectedSite] = useState<Site | SiteCard>();
  const [logoutError, setLogoutError] = useState("");
  const [signingOut, setSigningOut] = useState(false);
  const signOutPending = useRef(false);
  const showDemos = capabilities.paymentDemos;
  const canCreate = import.meta.env.MODE !== "community-demo" && (member.role === "admin" || member.role === "operator");

  async function signOut() {
    if (signOutPending.current) return;
    signOutPending.current = true;
    setLogoutError("");
    setSigningOut(true);
    try {
      if (endSession) await endSession();
      else await post<void>("/auth/logout", {}, token);
      signedOut();
    } catch (reason) {
      setLogoutError(message(reason));
    } finally {
      signOutPending.current = false;
      setSigningOut(false);
    }
  }

  function go(title: MerchantPage, order = "", pending = false) {
    setOrderRef(order);
    setPendingPayouts(pending);
    setStatementAccount("");
    setChannelTab("rates");
    setPage(title);
  }

  function labelFor(title: MerchantPage) {
    return title === "Payouts" ? t("Payout orders") : title === "Payments" ? t("Collection orders")
      : title === "API documentation" ? t("API docs")
      : production && title === "Channel accounts" ? (zh ? "通道与费率" : "Channels & rates")
      : title === "Start integration" ? (zh ? "开始接入" : "Start integration")
      : title === "Business reports" ? (zh ? "经营报表" : "Business reports") : t(title);
  }

  function navigation(title: MerchantPage) {
    if ((title === "Payment demos" && !showDemos) || (title === "API keys" && member.role !== "admin")) return null;
    const Icon = pages.find(item => item.title === title)!.icon;
    return <button key={title} type="button" className={page === title ? "active" : ""}
      aria-current={page === title ? "page" : undefined} onClick={() => go(title)}>
      <Icon size={21} aria-hidden="true" /><span>{labelFor(title)}</span>
    </button>;
  }

  const navItems = <>
      {production ? <>
        {navigation("Overview")}
        {navGroups.map(group => <div key={group.en} className="nav-group">
          <p>{zh ? group.zh : group.en}</p>
          {group.pages.map(navigation)}
        </div>)}
      </> : pages.filter(({ title }) => title !== "Start integration" && title !== "Business reports").map(({title})=>navigation(title))}
  </>;
  return <ConsoleShell workspace="merchant" title={t("Merchant workspace")} accountLabel={t("Merchant workspace")}
    accountName={member.name} logo={logo} header={<>{demoLinks ?? <strong>{t("Merchant workspace")}</strong>}
      <span className="muted small">{member.merchant_id} · {t(roles[member.role])}</span></>}
    environment={demoLinks ? (zh ? "只读演示" : "Read-only demo") : t(environment)}
    navigation={navItems} signingOut={signingOut} logout={signOut}>
    <main className="workspace">
      <div className="page-heading heading"><h1 ref={mainHeading} tabIndex={-1}>{labelFor(page)}</h1></div>
      {logoutError && <p className="error" role="alert">{t(logoutError)}</p>}
      {page === "Overview" && production && <>
        <ProductionFunds token={token} summaryOnly openSource={(direction, id) => go(direction === "collection" ? "Payments" : "Payouts", id)} />
        <MerchantHomeTasks token={token} canApprove={canCreate} openPayouts={() => go("Payouts", "", true)}
          openSettlement={() => go("Settlement")} openIntegration={() => go("Start integration")} openReports={() => go("Business reports")} />
      </>}
      {page === "Start integration" && production && <MerchantIntegration token={token} admin={member.role === "admin"} demos={showDemos} go={go} />}
      {((page === "Overview" && !production) || page === "Business reports") && <WorkspaceOverview capabilities={capabilities} token={token} openChannels={() => setPage("Channel accounts")} openPayments={() => setPage("Payments")} openSite={setSelectedSite} openSettlement={account => { setStatementAccount(account ?? ""); setPage("Settlement"); }} />}
      {page === "Payments" && <WorkspacePayments key={orderRef} token={token} initialOrder={orderRef} />}
      {page === "Payment demos" && showDemos && <PaymentDemos />}
      {page === "Team & security" && <div className="content-grid member-team">
        {member.role === "admin" ? <Team token={token} member={member} canManage={capabilities.teamManagement} /> :
          <section className="workspace-panel"><h2>{t("Team members")}</h2>
            <p className="empty muted">{t("An admin role is required to browse or manage the team.")}</p></section>}
        <TeamSecurity token={token} signedOut={signedOut} canEnroll={capabilities.mfaEnrollment} />
      </div>}
      {!production && page === "Sites" && <Sites token={token} canCreate={canCreate && capabilities.createSites}
        siteRouting={capabilities.siteRouting} open={setSelectedSite} />}
      {page === "Channel accounts" && <>
        {capabilities.siteRouting && <ChannelAccounts token={token} />}
        {production && <div className="header-actions merchant-channel-tabs" role="group" aria-label={zh ? "通道视图" : "Channel views"}>
          <button type="button" className={channelTab === "rates" ? "primary" : "secondary"} aria-pressed={channelTab === "rates"} onClick={() => setChannelTab("rates")}>{zh ? "产品与费率" : "Products and rates"}</button>
          <button type="button" className={channelTab === "history" ? "primary" : "secondary"} aria-pressed={channelTab === "history"} onClick={() => setChannelTab("history")}>{t("Collection routing")}</button>
        </div>}
        {channelTab === "rates" ? <MerchantRates token={token} siteRouting={capabilities.siteRouting} /> : <CollectionRouting token={token} accessHistory={capabilities.routeAccessHistory} />}
      </>}
      {page === "API keys" && member.role === "admin" && <APIKeys token={token} environment={environment} />}
      {page === "API documentation" && <MerchantDocs />}
      {page === "Collection routing" && <CollectionRouting token={token} accessHistory={capabilities.routeAccessHistory} />}
      {environment==="Production" && page==="API keys" && member.role==="admin" && <MerchantEvents token={token}/>}
      {page === "Chargebacks" && <Chargebacks token={token} available={capabilities.chargebacks} />}
      {page === "Payouts" && <Payouts key={orderRef + String(pendingPayouts)} initialOrder={orderRef} pendingOnly={pendingPayouts} token={token} member={member} workflow={environment === "Sandbox" && capabilities.payoutWorkflow} />}
      {page === "Settlement" && <Settlement canRequest={canCreate} token={token} canManage={canCreate && environment === "Sandbox"} initialAccount={statementAccount}
        reconciliation={capabilities.statementReconciliation} confirmations={capabilities.settlementConfirmations} />}
      {selectedSite && <Dialog drawer title={t("Site details")} close={() => setSelectedSite(undefined)}>
        <Details fields={[[t("Name"), selectedSite.name], [t("Site ID"), selectedSite.id],
          ...("methods" in selectedSite ? [[t("Status"), t(siteStatus(selectedSite))] as [string, string]] : []),
          ...(selectedSite.is_default ? [[t("Site"), t("Default site")] as [string, string]] : [])]} />
        <SiteFacts site={selectedSite} />
      </Dialog>}
    </main>
  </ConsoleShell>;
}

function MerchantIntegration({ token, admin, demos, go }: { token: string; admin: boolean; demos: boolean; go: (page: MerchantPage) => void }) {
  const { locale } = useLocale();
  const zh = locale === "zh-CN";
  const products = useWorkspaceData<IntegrationProduct[]>(base + "/channel-quotes", token);
  const progress = useWorkspaceData<{active_key:boolean; signature_checked_at:string|null; notification_configured:boolean; recorded_delivery:boolean; first_collection:string|null}>(base + "/integration-progress", token);
  const { dateTime } = useLocale();
  const items: { page: MerchantPage; title: string; copy: string }[] = [
    { page:"Channel accounts", title:zh?"核对通道与费率":"Check channels and rates", copy:zh?"查看已开通产品、币种、单笔范围和实际费率。":"Review enabled products, currencies, order limits and your rates." },
    { page:"API documentation", title:zh?"下载文档并验签":"Download the API contract and verify signing", copy:zh?"让开发人员先调用无资金验签接口。成功后再走代收和代付。":"Have your developer run the signature check before creating payment or payout orders." },
    ...(demos ? [{page:"Payment demos" as const,title:zh?"体验收银台":"Preview checkout",copy:zh?"用示例订单走一遍付款页面。":"Use a sample order to try the payment page."}] : []),
    ...(admin ? [{page:"API keys" as const,title:zh?"管理密钥和交易通知":"Manage API keys and notifications",copy:zh?"查看可用密钥，配置 HTTPS 通知地址并核对投递。":"Review API keys, set the HTTPS notification endpoint and check delivery."}] : []),
  ];
  return <section className="workspace-panel merchant-integration">
    <h2>{zh?"通道就绪状态":"Channel readiness"}</h2>
    <LoadState state={products} label={zh?"通道就绪状态":"Channel readiness"}>{rows => <div className="header-actions">
      {(["collection","payout"] as const).map(direction => {
        const matches = rows.filter(row => row.direction === direction);
        const ready = matches.some(row => row.ready);
        const label = direction === "collection" ? (zh?"代收":"Collection") : (zh?"代付":"Payout");
        return <span key={direction} className={"tag "+(ready?"green":"gray")}>{label} · {ready
          ? (zh?"可交易":"Ready for orders")
          : matches.length ? (zh?"已开通 · 待就绪":"Enabled · Not ready") : (zh?"待平台开通":"Awaiting platform activation")}</span>;
      })}
      {rows.length > 0 && <ul className="integration-products">
        {rows.map((row, index) => <li key={[row.product_id, row.direction, index].join("/")}>
          <strong>{row.product_id}</strong> · {row.market} · {row.currency}
          <div>{row.ready ? (zh ? "通道已就绪" : "Channel ready") : (zh ? "等待平台配置或上游就绪" : "Awaiting platform setup or upstream readiness")}</div>
        </li>)}
      </ul>}
    </div>}</LoadState>
    <LoadState state={progress} label={zh ? "接入进度" : "Integration progress"}>{state => <section className="integration-milestones">
      <h3>{zh ? "已确认的接入进度" : "Recorded integration progress"}</h3>
      <ul>
        <li>{state.active_key ? (zh ? "有可用密钥" : "Active API key available") : (zh ? "下一步：由平台签发 API 密钥" : "Next: ask the platform to issue an API key")}</li>
        <li>{state.signature_checked_at ? (zh ? "当前有效密钥已通过无资金验签 · " : "Signature check passed for an active key · ") + dateTime(state.signature_checked_at)
          : state.active_key ? (zh ? "下一步：商户开发人员运行无资金验签" : "Next: merchant developer runs the no-funds signature check")
          : (zh ? "等待平台签发密钥后验签" : "Signature check requires a key from the platform")}</li>
        <li>{state.notification_configured ? (state.recorded_delivery ? (zh ? "已记录当前地址的成功投递" : "Successful delivery recorded for the current endpoint") : (zh ? "已配置通知地址，待首次成功投递" : "Endpoint configured, awaiting first successful delivery")) : (zh ? "下一步：商户管理员配置交易通知" : "Next: merchant administrator configures notifications")}</li>
        <li>{state.first_collection ? (zh ? "首笔代收入账 · " : "First collection credited · ") + dateTime(state.first_collection) : (zh ? "首笔代收尚未入账" : "First collection credit not yet recorded")}</li>
      </ul>
    </section>}</LoadState>
    <ol>{items.map(item=><li key={item.page}>
      <button type="button" className="text-button" onClick={()=>go(item.page)}>{item.title}</button>
      <p className="muted">{item.copy}</p>
    </li>)}</ol>
    {!admin && <p className="muted">{zh?"密钥和交易通知由商户管理员管理。":"A merchant administrator manages API keys and notifications."}</p>}
  </section>;
}

function MemberStatus({ active }: { active: boolean }) {
  const { t } = useLocale();
  return <span className={active ? "tag green" : "tag gray"}>{active ? t("Active") : t("Disabled")}</span>;
}

function Team({ token, member, canManage }: { token: string; member: MemberSession; canManage: boolean }) {
  const { t } = useLocale();
  const team = useWorkspaceData<MemberSession[]>(base + "/members", token);
  const [selected, setSelected] = useState("");
  const [inviting, setInviting] = useState(false);
  const [busy, setBusy] = useState(false);
  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t("Team members")}</h2>
      {team.data && <div className="header-actions">
        <button className="secondary" onClick={team.reload}>{t("Refresh team")}</button>
        {canManage && <button className="primary" onClick={() => setInviting(true)}>{t("Invite member")}</button>}
      </div>}
    </div>
    {!canManage && <p className="muted panel-note">{t("Platform operations provisions the merchant administrator. Team invitations are unavailable.")}</p>}
    <LoadState state={team} label={t("team members")}>{members => members.length ?
      <WorkspaceTable label={t("Team members")} columns={[t("Member"), t("Role"), t("Status")]}>
        {members.map(item => <tr key={item.id}>
          <td><button className="text-button" onClick={() => setSelected(item.id)}>{item.name}</button>
            {item.id === member.id && <span className="muted small"> {t("(You)")}</span>}</td>
          <td>{t(roles[item.role])}</td><td><MemberStatus active={item.active} /></td>
        </tr>)}
      </WorkspaceTable> : <p className="muted empty">{t("There are no team members.")}</p>}</LoadState>
    {inviting && <Dialog drawer title={t("Invite a member")} locked={busy} close={() => setInviting(false)}>
      <Invitations token={token} onBusy={setBusy} invited={team.reload} />
    </Dialog>}
    {selected && <MemberDetail token={token} id={selected} canDisable={canManage && selected !== member.id}
      close={() => setSelected("")} changed={team.reload} />}
  </section>;
}

function MemberDetail({ token, id, canDisable, close, changed }: {
  token: string; id: string; canDisable: boolean; close: () => void; changed: () => void;
}) {
  const { t } = useLocale();
  // A colon is valid in a path segment and is part of a provisioned merchant login ID.
  const memberPath = "/members/" + encodeURIComponent(id).replaceAll("%3A", ":");
  const result = useWorkspaceData<MemberSession>(base + memberPath, token);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [updated, setUpdated] = useState<MemberSession>();
  const pending = useRef(false);

  async function disable() {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      setUpdated(await post<MemberSession>(memberPath + "/disable", {}, token));
      changed();
    } catch (reason) {
      setError(message(reason));
      result.reload();
      changed();
    } finally {
      pending.current = false;
      setConfirm(false);
      setBusy(false);
    }
  }

  return <Dialog drawer title={t("Member details")} locked={busy} close={close}>
    {error && <p role="alert" className="error">{t(error)}</p>}
    <LoadState state={result} label={t("member details")}>{data => {
      const item = updated ?? data;
      return <>
        <Details fields={[[t("Name"), item.name], [t("Member ID"), item.id], [t("Role"), t(roles[item.role])],
          [t("Status"), <MemberStatus active={item.active} />]]} />
        {canDisable && item.active && !item.protected && (confirm ? <div className="workspace-feedback">
          <p>{t("Disable this member? Their existing workspace sessions will lose access.")}</p>
          <div className="detail-actions"><button className="secondary" disabled={busy} onClick={() => setConfirm(false)}>{t("Cancel")}</button>
            <button className="danger secondary" disabled={busy} onClick={() => void disable()}>{busy ? t("Disabling…") : t("Confirm disable")}</button></div>
        </div> : <button className="danger secondary" onClick={() => setConfirm(true)}>{t("Disable member")}</button>)}
      </>;
    }}</LoadState>
  </Dialog>;
}

function Sites({ token, canCreate, siteRouting, open }: {
  token: string; canCreate: boolean; siteRouting: boolean; open: (site: Site | SiteCard) => void;
}) {
  const { t } = useLocale();
  const sites = useWorkspaceData<WorkspaceSetup>(base + "/setup", token);
  const [creating, setCreating] = useState(false);
  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t("Merchant sites")}</h2>
      {canCreate && <button className="primary" onClick={() => setCreating(true)}>{t("Create site")}</button>}
    </div>
    {siteRouting ? <><SiteExplanation />
      <p className="muted panel-note">{t("Platform operations assigns payment channels to your sites.")}</p>
    </> : <p className="muted panel-note">{t("Sites are optional records for your stores and websites. Production payment access, rates and settlement are managed at merchant level.")}</p>}
    {!canCreate && <p className="muted panel-note">{t("Your role has read-only access to sites.")}</p>}
    <LoadState state={sites} label={t("sites")}>{data =>
      <SiteCards sites={data.sites} open={open} action="View site details" />}</LoadState>
    {creating && <InventoryForm token={token} path={base + "/sites"} title={t("Create site")} close={() => setCreating(false)}
      created={() => { setCreating(false); sites.reload(); }}>
      {siteRouting && <p className="muted site-form-note">{t("Add a site when another store, website or business line needs its own payment setup. Your default site is ready for channel setup.")}</p>}
      <label htmlFor="site-name">{t("Site name")}</label><input id="site-name" name="name" required maxLength={100} />
      <label htmlFor="site-domain">{t("Business domain (optional)")}</label>
      <input id="site-domain" name="business_domain" maxLength={253} autoCapitalize="none" placeholder="shop.example.com" />
    </InventoryForm>}
  </section>;
}

function ChannelAccounts({ token }: { token: string }) {
  const { t } = useLocale();
  const accounts = useWorkspaceData<ChannelAccount[]>(base + "/channel-accounts", token);
  const setup = useWorkspaceData<WorkspaceSetup>(base + "/setup", token);
  const status = (account: ChannelAccount) => setup.data ? collectionAccountStatus(account,
    setup.data.effective_account_ids.includes(account.id)) : accountStatus(account);
  const sites = useWorkspaceData<Site[]>(base + "/sites", token);
  const [selected, setSelected] = useState("");
  const siteName = (id: string) => sites.data?.find(site => site.id === id)?.name ?? id;

  return <section className="workspace-panel">
      <div className="heading panel-heading"><h2>{t("Mock channel accounts")}</h2>
      </div>
      <p className="muted panel-note">{t("Platform operators configure and assign sandbox channels. This workspace shows the accounts assigned to your sites.")}</p>
      <LoadState state={accounts} label={t("channel accounts")}>{items => items.length ?
        <WorkspaceTable label={t("Mock channel accounts")} columns={[t("Account"), t("Site"), t("Status")]}>
          {items.map(account => <tr key={account.id}>
            <td><button className="text-button" onClick={() => setSelected(account.id)}>{channelTitle(account, t)}</button>
              <span className="channel-subtitle muted small">{channelSubtitle(account.provider, t)}</span></td>
            <td className="wrap">{siteName(account.site_id)}</td><td><span className={account.enabled ? "tag green" : "tag gray"}>{t(status(account))}</span></td>
          </tr>)}
        </WorkspaceTable> : <p className="muted empty">{t("There are no mock channel accounts in this workspace.")}</p>}</LoadState>
      {selected && <InventoryDetail<ChannelAccount> token={token} path={base + "/channel-accounts/" + encodeURIComponent(selected)}
        title={t("Channel account details")} close={() => setSelected("")} fields={account => [
          [t("Channel"), channelTitle(account, t)], [t("Account ID"), account.id], [t("Provider"), account.provider],
          ...(account.platform_channel_id ? [[t("Channel ID"), account.platform_channel_id] as [string, string]] : []),
          ...(account.platform_channel_name ? [[t("Stored channel name"), account.platform_channel_name] as [string, string]] : []),
          [t("Site"), siteName(account.site_id)],
          [t("Site ID"), account.site_id], [t("Status"), t(status(account))], [t("Environment"), t("Sandbox")],
        ]} />}
    </section>;
}

export function collectionAccountStatus(account: ChannelAccount, effective: boolean) {
  if (!account.enabled) return "Disabled";
  return effective ? "In effect" : "Assigned · awaiting routing";
}

export function accountStatus(account: ChannelAccount) {
  if (!account.enabled) return "Disabled";
  return account.routing_standby ? "Assigned · awaiting routing" : "Enabled";
}

function InventoryDetail<T>({ token, path, title, close, fields }: {
  token: string; path: string; title: string; close: () => void; fields: (item: T) => [string, ReactNode][];
}) {
  const detail = useWorkspaceData<T>(path, token);
  return <Dialog drawer title={title} close={close}>
    <LoadState state={detail} label={title}>{item => <Details fields={fields(item)} />}</LoadState>
  </Dialog>;
}
