import { useEffect, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";
import { InventoryForm, LoadState, useWorkspaceData } from "./WorkspaceInventoryUI";
import type { IssuedKey } from "./WorkspaceControls";
import type { PlatformSession } from "./PlatformWorkspace";

import { platformAPI, productionOperations } from "./platformEnvironment";

const path = platformAPI + "/merchants";

export interface PlatformMerchant { id: string; name: string; sites: number; accounts: number; active?: boolean; login_status?: string; collection_ready?: boolean; payout_ready?: boolean }

export function PlatformMerchantCards({ merchants, configure }: {
  merchants: PlatformMerchant[];
  configure: (id: string) => void;
}) {
  const { t, locale } = useLocale();
  const zh = locale === "zh-CN";
  const login = {active: t("Active"), pending: t("Awaiting first-time setup"), disabled: t("Disabled"), expired: t("Expired")};
  if (!merchants.length) return <p className="empty muted">{t("No merchants match your search.")}</p>;
  return <ul className="platform-card-grid platform-merchant-cards" role="list" aria-label={t("Merchants")}>
    {merchants.map(item => <li className="platform-card" key={item.id}>
      <div className="platform-card-identity">
        <h3><button type="button" className="text-button platform-card-link wrap" onClick={() => configure(item.id)}>
          {item.name}
        </button></h3>
        {item.login_status && <dl className="merchant-readiness">
          <div><dt>{zh ? "后台登录" : "Portal login"}</dt><dd>{login[item.login_status as keyof typeof login] ?? (zh ? "待配置" : "Setup required")}</dd></div>
          <div><dt>{t("Collection")}</dt><dd className={item.collection_ready ? "tag green" : "tag gray"}>{item.collection_ready ? t("Ready for orders") : (zh ? "不可交易" : "Unavailable")}</dd></div>
          <div><dt>{t("Payout")}</dt><dd className={item.payout_ready ? "tag green" : "tag gray"}>{item.payout_ready ? t("Ready for orders") : (zh ? "不可交易" : "Unavailable")}</dd></div>
        </dl>}
        <p className="muted small wrap">{t("Merchant ID")} · {item.id}</p>
      </div>
      <dl className="platform-card-meta">
        {!productionOperations && <div><dt>{t("Sites")}</dt><dd>{item.sites}</dd></div>}
        <div><dt>{t("Channel accounts")}</dt><dd>{item.accounts}</dd></div>
      </dl>
      <button className="secondary" type="button" onClick={() => configure(item.id)}
        aria-label={`${t("Manage merchant")} · ${item.name}`}>{t("Manage merchant")}</button>
    </li>)}
  </ul>;
}

export interface AdminInvitation {
  merchant_id: string;
  site_id: string;
  invite_version: number;
  invite_status: "pending" | "accepted" | "expired";
  expires_at: string;
  invite_path?: string;
}

import { ProductionCredentialDownload, productionCredentialContents, type ProductionKey } from "./ProductionCredentialDownload";

type MerchantOnboardingResult = AdminInvitation & {
  api_key?: IssuedKey | ProductionKey; email?: string; initial_password?: string; merchant_name?: string; admin_name?: string;
};

export function ProductionInvitationResult({ email, password, close, configure, merchant, apiKey, merchantName, administratorName, expires }: {
  email: string; password?: string; merchant?: string; apiKey?: ProductionKey; close: () => void; configure?: () => void;
  merchantName?: string; administratorName?: string; expires?: string;
}) {
  const { t, localizedLink, dateTime } = useLocale();
  const signIn = localizedLink("/flowxpay/ops/?portal=merchant");
  const complete = merchant && password && apiKey;
  const delivery = complete ? [
    t("Merchant onboarding details"),
    ...(merchantName ? [t("Merchant name") + " · " + merchantName] : []),
    t("Merchant ID") + " · " + merchant,
    ...(administratorName ? [t("Administrator name") + " · " + administratorName] : []),
    t("Merchant sign-in") + " · " + new URL(signIn, window.location.origin).href,
    t("Administrator email") + " · " + email,
    t("Initial password") + " · " + password,
    ...(expires ? [t("Initial password expires") + " · " + dateTime(expires) + " (" + expires + ")"] : []),
    t("First-time setup") + " · " + t("Set a new password and bind an authenticator from the sign-in page."),
    "",
    t("API base URL") + " · " + new URL("/flowxpay/v1", window.location.origin).href,
    t("Initial production API key"),
    productionCredentialContents(merchant, apiKey),
  ].join("\n") : "";
  return <Dialog drawer title={t(complete ? "Merchant account opened" : "Merchant administrator")} close={close}>
    <p className="wrap">{email}</p>
    {delivery && <CopyOnboardingDetails contents={delivery} />}
    {password ? <>
      <p>{t("Share the initial password privately with the administrator. It expires in 24 hours and can only be viewed here.")}</p>
      <label htmlFor="merchant-initial-password">{t("Initial password")}</label>
      <input id="merchant-initial-password" value={password} readOnly autoComplete="off" onFocus={e => e.currentTarget.select()} />
    </> : <p>{t("Account already opened. To replace an unused initial password, open the merchant configuration.")}</p>}
    {merchant && <p>{t("Merchant ID")} <code>{merchant}</code></p>}
    {apiKey && merchant && <section>
      <h3>{t("Initial production API key")}</h3>
      <ProductionCredentialDownload merchant={merchant} apiKey={apiKey} />
    </section>}
    <a className="text-button" href={signIn} target="_blank" rel="noreferrer">{t("Merchant sign-in")}</a>
    <div className="detail-actions">
      {configure && <button type="button" className="secondary" onClick={configure}>{t("Configure merchant")}</button>}
      <button type="button" className="primary" onClick={close}>{t("Done")}</button>
    </div>
  </Dialog>;
}

export function PlatformMerchants({ session, signOut, configure }: {
  session: PlatformSession; signOut: (notice: string) => void; configure: (id: string) => void;
}) {
  const { t } = useLocale();
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [pages, setPages] = useState([""]);
  const [revision, setRevision] = useState(0);
  const [creating, setCreating] = useState(false);

  function find(value: string) {
    setSearch(value);
    setQuery(value.trim());
    setPages([""]);
    setRevision(previous => previous + 1);
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    find(search);
  }

  return <section className="workspace-panel platform-control-panel">
    <div className="heading panel-heading">
      <form className="overview-filters platform-merchant-search" role="search" onSubmit={submitSearch}>
        <label htmlFor="platform-merchant-search">{t("Merchant name or ID")}</label>
        <input id="platform-merchant-search" type="search" value={search} maxLength={100}
          onChange={event => setSearch(event.target.value)} />
        <button className="secondary">{t("Search")}</button>
        {query && <button className="text-button" type="button" onClick={() => find("")}>{t("Clear")}</button>}
      </form>
      {session.role === "operator" && <button className="primary" onClick={() => setCreating(true)}>{t("Open merchant account")}</button>}
    </div>
    <MerchantResults key={JSON.stringify([query, pages, revision])} session={session} query={query}
      after={pages[pages.length - 1]} back={pages.length > 1 ? () => setPages(pages.slice(0, -1)) : undefined}
      next={cursor => setPages([...pages, cursor])} signOut={signOut} configure={configure} />
    {creating && <MerchantOnboarding session={session} close={() => setCreating(false)}
      created={find} configure={configure} />}
  </section>;
}

export function MerchantOnboarding({ session, close, created, configure }: {
  session: PlatformSession; close: () => void; created: (id: string) => void; configure: (id: string) => void;
}) {
  const { t } = useLocale();
  const [result, setResult] = useState<MerchantOnboardingResult>();
  if (result && productionOperations) return <ProductionInvitationResult email={result.email ?? ""} password={result.initial_password} merchant={result.merchant_id}
    apiKey={result.api_key && "private_key_pem" in result.api_key ? result.api_key : undefined}
    merchantName={result.merchant_name} administratorName={result.admin_name} expires={result.expires_at}
    close={close} configure={() => { close(); configure(result.merchant_id); }} />;
  if (result) return <AdminInvitationResult invitation={result} apiKey={result.api_key && "secret" in result.api_key ? result.api_key : undefined} close={close}
    configure={() => { close(); configure(result.merchant_id); }} />;
  return <InventoryForm<MerchantOnboardingResult> token={session.token} path={productionOperations ? platformAPI + "/onboard" : path} title={t("Open merchant account")}
    submitLabel="Open merchant account" onboardingAuthenticator={productionOperations} close={close} created={value => {
      if (value) {
        setResult(value);
        created(value.merchant_id);
      }
    }}>
    <OnboardingFields />
  </InventoryForm>;
}

function OnboardingFields() {
  const { t } = useLocale();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [site, setSite] = useState<string>();
  const [admin, setAdmin] = useState<string>();
  const emailName = email.trim().toLowerCase();
  const at = emailName.lastIndexOf("@");
  const defaultAdmin = at > 0 ? Array.from(emailName.slice(0, at)).slice(0, 100).join("") : "";

  return <>
    <label htmlFor="onboard-name">{t("Merchant name")}</label>
    <input id="onboard-name" name="name" data-dialog-focus required maxLength={100}
      value={name} onChange={event => setName(event.target.value)} />
    <label htmlFor="onboard-email">{t("Administrator email")}</label>
    <input id="onboard-email" name="admin_email" type="email" required maxLength={254}
      autoComplete="off" aria-describedby="onboard-email-help" value={email} onChange={event => setEmail(event.target.value)} />
    <p id="onboard-email-help" className="muted small">{t("Use the administrator’s contact email. They will use it to sign in.")}</p>
    <details className="form-options">
      <summary>{t(productionOperations ? "Customize administrator name" : "Customize site and administrator names")}</summary>
      <div className="workspace-fields">
        {!productionOperations && <><label htmlFor="onboard-site">{t("Default site name")}</label>
        <input id="onboard-site" name={site === undefined ? undefined : "site_name"} maxLength={100} placeholder={name.trim()}
          value={site ?? name.trim()} onChange={event => setSite(event.target.value)} /></>}
        <label htmlFor="onboard-admin">{t("Administrator name")}</label>
        <input id="onboard-admin" name={admin === undefined ? undefined : "admin_name"} maxLength={100} placeholder={defaultAdmin}
          value={admin ?? defaultAdmin} onChange={event => setAdmin(event.target.value)} />
      </div>
    </details>
  </>;
}

function MerchantResults({ session, query, after, next, back, signOut, configure }: {
  session: PlatformSession; query: string; after: string;
  next: (after: string) => void; back?: () => void;
  signOut: (notice: string) => void; configure: (id: string) => void;
}) {
  const { t } = useLocale();
  const state = useWorkspaceData<{ merchants: PlatformMerchant[]; next_after?: string }>(
    path + "?" + new URLSearchParams({ q: query, after }), session.token);
  useEffect(() => {
    if (state.error instanceof ApiError && state.error.status === 401) signOut("An active platform preview session is required.");
  }, [state.error, signOut]);
  return <LoadState state={state} label={t("Merchants")}>{({ merchants, next_after: cursor }) => <>
    <PlatformMerchantCards merchants={merchants} configure={configure} />
    {(back || cursor) && <div className="overview-footer">
      {back && <button className="secondary" onClick={back}>{t("Previous page")}</button>}
      {cursor && <button className="secondary" onClick={() => next(cursor)}>{t("Next page")}</button>}
    </div>}
  </>}</LoadState>;
}

export function AdminInvitationResult({ invitation, apiKey, close, configure }: {
  invitation: AdminInvitation; apiKey?: IssuedKey; close: () => void; configure?: () => void;
}) {
  const { t, localizedLink, dateTime } = useLocale();
  const { invite_path: link, merchant_id: merchant, expires_at: expiry } = invitation;
  const inviteURL = link ? window.location.origin + localizedLink(link) : "";
  const delivery = configure && apiKey && inviteURL ? [
    t("Merchant onboarding details") + " · " + t("Sandbox"),
    t("Merchant ID") + " · " + merchant,
    t("Invitation link") + " · " + inviteURL,
    t("Expires") + " · " + dateTime(expiry),
    t("API key secret") + " · " + apiKey.secret,
  ].join("\n") : "";
  return <Dialog drawer title={t(configure ? "Merchant account opened" : "Administrator invitation")} close={close}>
    <div className="payment-form">
      <p className="wrap">{t("Merchant ID")} · {merchant}</p>
      {delivery && <CopyOnboardingDetails contents={delivery} />}
      {configure && <section className="result-section">
        <h3>{t("Initial sandbox API key")}</h3>
        {apiKey ? <>
          <p>{t("Copy this sandbox key now. This is the only time its secret is shown.")}</p>
          <OneTimeValue id="onboard-api-key" label={t("API key secret")} copyLabel={t("Copy sandbox API key")} value={apiKey.secret} />
        </> : <p>{t("Use Operations to manage the merchant’s sandbox API keys.")}</p>}
      </section>}
      <section className="result-section">
        <h3>{t("Merchant workspace activation")}</h3>
        {link ? <>
          <p>{t("Share this one-time link with the administrator so they can set a password and sign in with the Merchant admin role.")}</p>
          <OneTimeValue id="onboard-invite-link" label={t("Invitation link")} copyLabel={t("Copy sandbox invitation link")}
            value={inviteURL} />
          <span className="muted small">{t("Expires")} {dateTime(expiry)}</span>
        </> : <p>{t(invitation.invite_status === "accepted" ? "The administrator has already accepted the invitation."
          : "This invitation link is shown only when issued. You can generate a replacement in Operations.")}</p>}
      </section>
      {configure && <section className="result-section">
        <h3>{t("Merchant configuration")}</h3>
        <p>{t("You can configure the merchant now and replace a pending invitation later in Operations.")}</p>
      </section>}
      <div className="detail-actions">
        {configure ? <>
          <button type="button" className="secondary" onClick={close}>{t("Done")}</button>
          <button type="button" className="primary" onClick={configure}>{t("Configure merchant")}</button>
        </> : <button type="button" className="primary" onClick={close}>{t("Done")}</button>}
      </div>
    </div>
  </Dialog>;
}

function CopyOnboardingDetails({ contents }: { contents: string }) {
  const { t } = useLocale();
  const [status, setStatus] = useState<"copied" | "manual">();
  async function copy() {
    try {
      await navigator.clipboard.writeText(contents);
      setStatus("copied");
    } catch {
      setStatus("manual");
    }
  }
  return <section className="result-section">
    <p>{t("Send these onboarding details privately to the merchant.")}</p>
    <button type="button" className="primary" onClick={() => void copy()}>{t("Copy all onboarding details")}</button>
    <p role="status">{status === "copied" && t("Onboarding details copied.")}</p>
    {status === "manual" && <label>{t("Select all and copy")}
      <textarea readOnly rows={8} value={contents} onFocus={event => event.currentTarget.select()} />
    </label>}
  </section>;
}

function OneTimeValue({ id, label, copyLabel, value }: { id: string; label: string; copyLabel: string; value: string }) {
  const { t } = useLocale();
  const [status, setStatus] = useState<"copied" | "manual">();

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setStatus("copied");
    } catch {
      setStatus("manual");
    }
  }

  return <>
    <label htmlFor={id}>{label}</label>
    <input id={id} readOnly value={value} autoComplete="off" spellCheck={false}
      onFocus={event => event.currentTarget.select()} />
    <div className="detail-actions">
      <button type="button" className="secondary" aria-describedby={id + "-copy-status"} onClick={copy}>{copyLabel}</button>
      <span role="status" id={id + "-copy-status"} className="muted small">
        {status && t(status === "copied" ? "Copied" : "Select the field to copy it manually.")}
      </span>
    </div>
  </>;
}

export function MerchantAdminInvitation({ session, invitation, refresh }: {
  session: PlatformSession; invitation: AdminInvitation; refresh: () => void;
}) {
  const { t, dateTime } = useLocale();
  const [reissuing, setReissuing] = useState(false);
  const [issued, setIssued] = useState<AdminInvitation>();
  const status = { pending: "Awaiting activation", expired: "Invitation expired", accepted: "Administrator activated" } as const;
  const close = () => { setReissuing(false); refresh(); };
  return <div>
    <h3>{t("Merchant workspace activation")}</h3>
    <p>{t(status[invitation.invite_status])}
      {invitation.invite_status === "pending" && <span className="muted small"> · {t("Expires")} {dateTime(invitation.expires_at)}</span>}
    </p>
    {session.role === "operator" && invitation.invite_status !== "accepted" &&
      <button className="secondary setup-invite-action" onClick={() => setReissuing(true)}>{t("Replace invitation")}</button>}
    {reissuing && <InventoryForm<AdminInvitation> token={session.token}
      path={path + "/" + encodeURIComponent(invitation.merchant_id) + "/admin-invitation"}
      title={t("Replace invitation")} submitLabel="Replace invitation"
      payload={() => ({ invite_version: invitation.invite_version })} close={close} created={result => {
        close();
        setIssued(result);
      }}>
      <p>{t("Replacing the invitation invalidates the previous link and creates a new one for the same administrator.")}</p>
    </InventoryForm>}
    {issued && <AdminInvitationResult invitation={issued} close={() => setIssued(undefined)} />}
  </div>;
}
