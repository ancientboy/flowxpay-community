import { sandboxCapabilities } from "./memberEnvironment";
import { OrderQuote } from "./ChannelPricing";
import { LanguageSelect, translate, useLocale } from "./locale";
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  ArrowLeft, ArrowRight, ArrowSquareOut, BookOpen, CaretRight, CreditCard, Clock,
  WarningCircle, House, MagnifyingGlass, Plus, Stack, Wallet, X,
} from "@phosphor-icons/react";
import { type Capability, type Payment, type Workspace, ApiError, parseMinorUnits, request } from "./api";
import { Checkout } from "./Checkout";
import { WorkspaceSignIn, type MemberSession } from "./WorkspaceAccess";
import { PaymentDemos } from "./PaymentDemos";
import { MerchantDocs } from "./MerchantDocs";
import { MemberWorkspace } from "./MemberWorkspace";
import { PlatformWorkspace } from "./PlatformWorkspace";
import { Dialog } from "./Dialog";
import { CollectionExceptions, PaymentStatusBadge, Refunds } from "./CollectionEvents";

const navigation = [
  { name: "Overview", icon: House },
  { name: "Payments", icon: CreditCard },
  { name: "Exceptions", icon: WarningCircle },
  { name: "API documentation", icon: BookOpen },
];

type PaymentRequest = Capability & { site_id: string; merchant_reference: string; amount_minor: number };

const methodName = { pix: "Pix", card: "Card", gateway: "India gateway" };


function Logo() {
  return <div className="brand-wrap"><strong className="community-wordmark">FlowXPay</strong></div>;
}

function Connect({ connect, back }: { connect: (key: string, workspace: Workspace) => void; back: () => void }) {
  const { t } = useLocale();
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function open(event: FormEvent) {
    event.preventDefault();
    setError("");
    setBusy(true);
    try { connect(key.trim(), await request<Workspace>("/v1/capabilities", key.trim())); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not open the sandbox."); }
    finally { setBusy(false); }
  }
  return (
    <main className="connect-shell">
      <section className="connect"><div className="access-header"><Logo /><LanguageSelect /></div><span className="tag blue">{t("Sandbox")}</span>
        <h1>{t("Payments workspace")}</h1>
        <p className="muted access-description">{t("Use a sandbox API key to view and create test orders.")}</p>
        <form onSubmit={open}>
          <label htmlFor="key">{t("Sandbox API key")}</label>
          <input id="key" autoComplete="off" type="password" value={key} onChange={event => setKey(event.target.value)} required />
          {error && <p className="error" role="alert">{t(error)}</p>}
          <button className="primary full" disabled={busy}>{busy ? t("Connecting…") : t("Open workspace")}</button>
        </form>
        <div className="access-links"><button className="text-button" type="button" disabled={busy} onClick={back}>{t("Back to sign-in")}</button></div>
      </section>
    </main>
  );
}

function NewPayment({ keyValue, sites, complete, onBusy }: {
  keyValue: string; sites: Workspace["sites"]; complete: (p: Payment) => void;
  onBusy: (value: boolean) => void;
}) {
  const { t } = useLocale();
  const [siteID, setSiteID] = useState(sites[0]?.id ?? "");
  const [choice, setChoice] = useState(0);
  const options = sites.find(site => site.id === siteID)?.payments ?? [];
  const option = options[choice];
  const [reference, setReference] = useState("");
  const [amount, setAmount] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const pending = useRef(false);
  const attempt = useRef<{ fields: PaymentRequest; payload: string; id: string } | undefined>(undefined);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    if (pending.current || !option) return;
    const minor = parseMinorUnits(amount);
    if (minor === undefined) {
      setError("Enter an amount with up to two decimal places.");
      return;
    }
    if (minor <= 0) {
      setError("Enter an amount greater than zero.");
      return;
    }
    const fields = { ...option, site_id: siteID, merchant_reference: reference.trim(), amount_minor: minor };
    const payload = JSON.stringify(fields);
    if (!uncertain && attempt.current?.payload !== payload) {
      attempt.current = { fields, payload, id: crypto.randomUUID() };
    }
    if (!attempt.current) return;
    pending.current = true;
    setBusy(true);
    onBusy(true);
    try {
      const payment = await request<Payment>("/v1/payments", keyValue, {
        method: "POST", headers: { "Idempotency-Key": attempt.current.id }, body: attempt.current.payload,
      });
      complete(payment);
    } catch (reason) {
      if (uncertain || !(reason instanceof ApiError) || reason.status >= 500) {
        setUncertain(true);
        setError("Creation could not be confirmed. Retry the same request or check whether this reference already exists.");
      } else {
        setError(reason.message);
      }
    } finally {
      pending.current = false;
      setBusy(false);
      onBusy(false);
    }
  }
  async function checkByReference() {
    if (pending.current || !attempt.current) return;
    const { fields } = attempt.current;
    pending.current = true;
    setBusy(true);
    onBusy(true);
    try {
      const query = new URLSearchParams({ merchant_reference: fields.merchant_reference });
      const result = await request<{ data: Payment[] }>(`/v1/payments?${query}`, keyValue);
      const payment = result.data[0];
      if (!payment) {
        setError("This reference has no visible order yet. You can retry the same request.");
      } else if (!Object.entries(fields).every(([field, value]) => payment[field as keyof PaymentRequest] === value)) {
        setError("An order with this reference has different payment details. Review it in Payments.");
      } else {
        complete(payment);
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not check this reference.");
    } finally {
      pending.current = false;
      setBusy(false);
      onBusy(false);
    }
  }
  return (
    <form className="payment-form" onSubmit={submit}>
      <label htmlFor="payment-site">{t("Site")}</label>
      <select id="payment-site" disabled={busy || uncertain} value={siteID} onChange={event => { setSiteID(event.target.value); setChoice(0); }}>
        {sites.map(site => <option key={site.id} value={site.id}>{site.name}</option>)}
      </select>
      <label htmlFor="method">{t("Payment method")}</label>
      <select id="method" value={option ? choice : ""} disabled={busy || uncertain || !option} onChange={event => setChoice(Number(event.target.value))}>
        {!option && <option value="">{t("No enabled methods")}</option>}
        {options.map((option, index) => <option key={option.method} value={index}>{t(methodName[option.method])} · {option.market} · {option.currency}</option>)}
      </select>
      <label htmlFor="reference">{t("Merchant reference")}</label>
      <input id="reference" disabled={busy || uncertain} value={reference} required maxLength={80} pattern="[a-zA-Z0-9._:\-]+" onChange={event => setReference(event.target.value)} placeholder="ORDER-10424" />
      <label htmlFor="amount">{t("Amount")}{option ? ` (${option.currency})` : ""}</label>
      <input id="amount" disabled={busy || uncertain} value={amount} inputMode="decimal" required onChange={event => setAmount(event.target.value)} placeholder="249.00" />
      {error && <p className="error" role="alert">{t(error)}</p>}
      <button className="primary full" disabled={busy || !option}>{busy ? t("Working…") : uncertain ? t("Retry same request") : t("Create payment")}</button>
      {uncertain && <button className="secondary full" type="button" disabled={busy}
        onClick={() => void checkByReference()}>{t("Check by reference")}</button>}
    </form>
  );
}

function Dashboard({ access, disconnect }: { access: { key: string; workspace: Workspace }; disconnect: () => void }) {
  const { t, money, dateTime, localizedLink } = useLocale();
  const { key: keyValue, workspace } = access;
  const [page, setPage] = useState("Overview");
  const [payments, setPayments] = useState<Payment[]>([]);
  const [selected, setSelected] = useState<Payment>();
  const [creating, setCreating] = useState(false);
  const [paymentSubmitting, setPaymentSubmitting] = useState(false);
  const [search, setSearch] = useState("");
  const [currency, setCurrency] = useState<Capability["currency"]>("USD");
  const [pendingOnly, setPendingOnly] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  async function reload() {
    setError("");
    try { setPayments((await request<{ data: Payment[] }>("/v1/payments", keyValue)).data); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Unable to load orders."); }
    finally { setLoading(false); }
  }
  useEffect(() => { void reload(); }, [keyValue]);

  async function open(paymentID: string) {
    setError("");
    try { setSelected(await request<Payment>(`/v1/payments/${encodeURIComponent(paymentID)}`, keyValue)); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Unable to open this order."); }
  }

  const paid = payments.filter(payment => payment.status === "succeeded" && payment.currency === currency);
  const pending = payments.filter(payment => payment.status === "requires_action");
  const term = search.trim().toLowerCase();
  const filtered = payments.filter(payment =>
    (!pendingOnly || payment.status === "requires_action") &&
    (payment.merchant_reference.toLowerCase().includes(term) || payment.id.toLowerCase().includes(term)),
  );

  return (
    <div className="app-shell">
      <Logo />
      <header className="topbar">
        <div className="identity"><span className="muted small">{t("Workspace")}</span><strong>{workspace.merchant.name}</strong></div>
        <span className="tag blue"><i aria-hidden="true" />{t("Sandbox")}</span>
        <button className="secondary disconnect" onClick={disconnect} aria-label={t("Disconnect sandbox")}><ArrowLeft size={16} /><span>{t("Disconnect")}</span></button>
        <LanguageSelect />
        <button className="primary" onClick={() => workspace.request_signing_required ? setPage("API documentation") : setCreating(true)}><Plus size={18} aria-hidden="true" />{t(workspace.request_signing_required ? "Signed API setup" : "Create payment")}</button>
      </header>
      <nav className="nav" aria-label={t("Merchant workspace")}>
        {navigation.map(({ name, icon: Icon }) =>
          <button key={name} type="button" className={page === name ? "active" : ""} aria-current={page === name ? "page" : undefined}
            onClick={() => { setPage(name); setPendingOnly(false); setSearch(""); }}>
            <Icon size={23} aria-hidden="true" /><span>{t(name === "API documentation" ? "API docs" : name)}</span>
          </button>)}
      </nav>
      <main className="workspace">
        <div className="page-heading heading"><h1>{t(page === "Payments" ? "Payment list" : page)}</h1><div className="header-actions">
          <span className="muted small">{workspace.merchant.id}</span>
          {page !== "Exceptions" && page !== "API documentation" && <button className="secondary" onClick={() => void reload()}>{t("Refresh")}</button>}
        </div></div>
        {page === "Overview" && <section className="metrics" aria-label={t("Recent payment metrics")}>
          <div className="metric-collected"><span className="metric-label"><Wallet size={19} aria-hidden="true" />{t("Collected")}</span><strong>{money(paid.reduce((sum, payment) => sum + payment.amount_minor, 0), currency)}</strong></div>
          <div><span className="metric-label"><CreditCard size={19} aria-hidden="true" />{t("Payments")}</span><strong>{payments.length}</strong></div>
          <div><span className="metric-label"><Clock size={19} aria-hidden="true" />{t("Awaiting payment")}</span><strong>{pending.length}</strong></div>
          <div className="metric-filter"><label htmlFor="currency">{t("Collected currency")}</label>
            <select id="currency" value={currency} onChange={event => setCurrency(event.target.value as Capability["currency"])}><option>USD</option><option>BRL</option><option>INR</option></select>
          </div>
        </section>}
        {error && <p className="error" role="alert">{t(error)}</p>}
        {page === "API documentation" ? <MerchantDocs /> : page === "Exceptions" ? <CollectionExceptions keyValue={keyValue} open={id => void open(id)} /> :
        <div className={page === "Overview" ? "content-grid" : ""}>
          <section className="orders" aria-label={t("Payment list")}>
            {page === "Overview" && <div className="panel-heading heading">
              <h2>{t("Recent transactions")}</h2>
              <button className="text-button panel-link" onClick={() => { setPage("Payments"); setPendingOnly(false); setSearch(""); }}>{t("View all payments")}<ArrowRight size={16} aria-hidden="true" /></button>
            </div>}
            <div className="search">
              <MagnifyingGlass size={20} aria-hidden="true" />
              <input aria-label={t("Search payments")} placeholder={t("Search by reference")} value={search} onChange={event => setSearch(event.target.value)} />
            </div>
            {pendingOnly && <button className="secondary filter" onClick={() => setPendingOnly(false)}>{t("Awaiting payment")} <X size={14} /></button>}
            {loading ? <p role="status" className="muted">{t("Loading payments…")}</p> : (
              <div className="table-scroll" tabIndex={0} role="region" aria-label={t("Payment table")}><table>
                <thead><tr><th scope="col">{t("Reference")}</th><th scope="col" className="numeric">{t("Amount")}</th><th scope="col">{t("Status")}</th><th scope="col">{t("Method")}</th></tr></thead>
                <tbody>{filtered.map(payment => <tr key={payment.id}>
                  <td><button className="text-button" onClick={() => void open(payment.id)}>{payment.merchant_reference}</button></td>
                  <td className="numeric">{money(payment.amount_minor, payment.currency)}</td>
                  <td><PaymentStatusBadge payment={payment} /></td>
                  <td>{t(methodName[payment.method])} ({payment.currency})</td>
                </tr>)}</tbody>
              </table></div>
            )}
            {!loading && filtered.length === 0 && <p className="muted empty">{payments.length ? t("No payments match your search.") : t("Create a sandbox payment to start.")}</p>}
            {payments.length >= 100 && <p className="small muted">{t("Showing the latest 100 payments.")}</p>}
          </section>
          {page === "Overview" && <aside className="queue" aria-label={t("Action required")}>
            <h2>{t("Action required")}</h2>
            <button className="task" onClick={() => { setPage("Payments"); setSearch(""); setPendingOnly(true); }}>
              <Stack size={24} aria-hidden="true" /><span>{t("Review pending payments")}</span><strong>{pending.length}</strong><CaretRight size={16} />
            </button>
          </aside>}
        </div>}
      </main>
      {creating && <Dialog title={t("Create sandbox payment")} locked={paymentSubmitting} close={() => setCreating(false)}>
        <NewPayment keyValue={keyValue} sites={workspace.sites} onBusy={setPaymentSubmitting} complete={payment => {
          setCreating(false); setSelected(payment); void reload();
        }} />
      </Dialog>}
      {selected && <Dialog title={t("Payment details")} close={() => setSelected(undefined)}>
        <div className="payment-detail">
          <div className="detail-total"><strong>{money(selected.amount_minor, selected.currency)}</strong><PaymentStatusBadge payment={selected} /></div>
          <dl>
            <dt>{t("Merchant reference")}</dt><dd>{selected.merchant_reference}</dd>
            <dt>{t("Platform reference")}</dt><dd className="wrap">{selected.id}</dd>
            <dt>{t("Method")}</dt><dd>{t(methodName[selected.method])} · {selected.market}</dd>
            <dt>{t("Quoted merchant fee")}</dt><dd><OrderQuote quote={selected.quote} currency={selected.currency} /></dd>
            <dt>{t("Created")}</dt><dd>{dateTime(selected.created_at)}</dd>
            {selected.paid_at && <><dt>{t("Paid")}</dt><dd>{dateTime(selected.paid_at)}</dd></>}
          </dl>
          {selected.failure_code && <p className="muted" role="status">{{
            channel_confirmation_pending: t("The channel result is pending. Refresh this payment to check its status."),
            channel_action_unsupported: t("The simulated gateway returned an unsupported action."),
            channel_rate_limited: t("The channel declined this request because of its rate limit."),
            channel_rejected: t("The channel declined this request."),
          }[selected.failure_code] ?? selected.failure_code}</p>}
          {error && <p className="error" role="alert">{t(error)}</p>}
          <div className="detail-actions">
            {selected.checkout_url && <a className="primary" href={localizedLink(selected.checkout_url)} target="_blank" rel="noopener noreferrer">{t("Open checkout")} <ArrowSquareOut size={17} /></a>}
            <button className="secondary" onClick={() => { void open(selected.id); void reload(); }}>{t("Refresh status")}</button>
          </div>
          {selected.status === "succeeded" && selected.route_snapshot && <Refunds key={selected.id} keyValue={keyValue} payment={selected} signedWrites={workspace.request_signing_required} />}
        </div>
      </Dialog>}
    </div>
  );
}

function ReadOnlyShowcase() {
  const { t } = useLocale();
  const [docs, setDocs] = useState(new URLSearchParams(location.search).get("section") === "api");
  return <main className="release-showcase">
    <header><Logo /><LanguageSelect /></header>
    <h1>{t(docs ? "API documentation" : "Payment demos")}</h1>
    <p className="muted">{t("Read-only preview. Merchant access and real transactions are closed.")}</p>
    <nav aria-label={t("Preview sections")}>
      <button className={docs ? "secondary" : "primary"} type="button" aria-pressed={!docs} onClick={() => setDocs(false)}>{t("Payment demos")}</button>
      <button className={docs ? "primary" : "secondary"} type="button" aria-pressed={docs} onClick={() => setDocs(true)}>{t("API documentation")}</button>
    </nav>
    {docs ? <MerchantDocs /> : <PaymentDemos />}
  </main>;
}

export function App() {
  const [access, setAccess] = useState<{ key: string; workspace: Workspace }>();
  const [memberAccess, setMemberAccess] = useState<{ token: string; member: MemberSession }>();
  const [sandbox, setSandbox] = useState(false);
  const [notice, setNotice] = useState("");
  const path = window.location.pathname.match(/^\/checkout\/([a-zA-Z0-9_.-]+)(\/pay)?\/?$/);
  const { locale } = useLocale();
  const checkout = Boolean(path);
  const showcase = /^(?:\/flowxpay)?\/showcase\/?$/.test(window.location.pathname);
  const operations = /^\/ops\/?$/.test(window.location.pathname);
  useEffect(() => {
    document.title = checkout ? translate(locale, "FlowXPay checkout") : "FlowXPay | " + translate(locale, showcase ? "Payment demos" : operations ? "Platform operations" : "Merchant workspace");
  }, [locale, checkout, operations, showcase]);
  if (showcase) return <ReadOnlyShowcase />;
  if (operations) return <PlatformWorkspace logo={<Logo />} />;
  if (path) return <Checkout token={path[1]} handoff={Boolean(path[2])} />;
  if (memberAccess) return <MemberWorkspace logo={<Logo />} capabilities={sandboxCapabilities} {...memberAccess} signedOut={status => {
    setMemberAccess(undefined);
    setNotice(status ?? "");
  }} />;
  if (access) return <Dashboard access={access} disconnect={() => setAccess(undefined)} />;
  if (sandbox) return <Connect connect={(key, workspace) => setAccess({ key, workspace })} back={() => setSandbox(false)} />;
  return <WorkspaceSignIn logo={<Logo />} notice={notice} openSandbox={() => setSandbox(true)}
    signedIn={(token, member) => { setNotice(""); setMemberAccess({ token, member }); }} />;
}
