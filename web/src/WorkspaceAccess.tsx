import { LanguageSelect, useLocale } from "./locale";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError, request } from "./api";

import { memberAPI, memberPortal, memberLogin } from "./memberEnvironment";
export const base = memberAPI;

export interface MemberSession {
  id: string;
  merchant_id: string;
  name: string;
  role: "admin" | "operator" | "viewer";
  active: boolean;
  protected?: boolean;
}

interface SecurityStatus {
  password_set: boolean;
  mfa_enabled: boolean;
}

export function post<T>(path: string, data: object, token?: string) {
  return request<T>(base + path, token, { method: "POST", body: JSON.stringify(data) });
}

export function message(reason: unknown) {
  return reason instanceof Error ? reason.message : "The request could not be completed.";
}

export function WorkspaceSignIn({ logo, signedIn, openSandbox, notice }: {
  logo: ReactNode;
  signedIn: (token: string, member: MemberSession) => void;
  openSandbox: () => void;
  notice: string;
}) {
  const { t, localizedLink } = useLocale();
  const [invitation, setInvitation] = useState(() =>
    window.location.pathname === "/invite" ? window.location.hash.slice(1) : "",
  );
  const latestInvitation = useRef(invitation);
  const [accepting, setAccepting] = useState(() => window.location.pathname === "/invite");
  const [bootstrap, setBootstrap] = useState(false);
  const [challenge, setChallenge] = useState("");
  const [merchant, setMerchant] = useState("");
  const [email, setEmail] = useState("");
  const [feedback, setFeedback] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    function captureInvitation() {
      if (window.location.pathname !== "/invite" || !window.location.hash) return;
      const token = window.location.hash.slice(1);
      latestInvitation.current = token;
      setInvitation(token);
      setError("");
      setFeedback("");
      window.history.replaceState(null, "", localizedLink("/invite"));
    }

    captureInvitation();
    window.addEventListener("hashchange", captureInvitation);
    return () => window.removeEventListener("hashchange", captureInvitation);
  }, [localizedLink]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const fields = new FormData(event.currentTarget);
    setError("");
    setFeedback("");
    setBusy(true);
    try {
      if (accepting) {
        const accepted = await post<{ merchant_id: string; email: string }>("/invitations/accept", {
          token: invitation, password: fields.get("password"),
        });
        if (latestInvitation.current !== invitation) return;
        latestInvitation.current = "";
        setInvitation("");
        setMerchant(accepted.merchant_id);
        setEmail(accepted.email);
        window.history.replaceState(null, "", localizedLink("/"));
        setAccepting(false);
        setFeedback("Invitation accepted. Sign in with your new password.");
        return;
      }

      let token: string;
      if (bootstrap) {
        token = String(fields.get("session")).trim();
      } else if (challenge) {
        const result = await post<{ session_token: string }>("/auth/mfa", {
          challenge_token: challenge, code: fields.get("code"),
        });
        token = result.session_token;
      } else {
        const result = await post<{ session_token: string } | { challenge_token: string }>("/auth/login", {
          merchant_id: merchant, email, password: fields.get("password"),
        });
        if ("challenge_token" in result) {
          setChallenge(result.challenge_token);
          return;
        }
        token = result.session_token;
      }
      signedIn(token, await request<MemberSession>(base + "/session", token));
    } catch (reason) {
      if (!accepting || latestInvitation.current === invitation) setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  function backToSignIn() {
    latestInvitation.current = "";
    setInvitation("");
    setAccepting(false);
    setBootstrap(false);
    setChallenge("");
    setError("");
    setFeedback("");
    window.history.replaceState(null, "", localizedLink("/"));
  }

  const stage = accepting ? "invite" : bootstrap ? "bootstrap" : challenge ? "mfa" : "password";
  const status = feedback || (stage === "password" ? notice : "");
  const title = { invite: t("Accept workspace invitation"), bootstrap: t("Open a local member session"),
    mfa: t("Verify your sign-in"), password: t("Merchant workspace") }[stage];

  return (
    <main className="connect-shell">
      <section className="connect">
        <div className="access-header">{logo}<LanguageSelect /></div><span className="tag blue">{t("Sandbox")}</span>
        <h1>{title}</h1>
        {stage === "password" && <p className="muted access-description">{t("Sign in with a merchant team account to manage your orders, team and settlements.")}</p>}
        {status && <p role="status" className="muted">{t(status)}</p>}
        {accepting && !invitation ? <p role="alert" className="error">{t("Open the full sandbox invitation link again.")}</p> : (
          <form key={accepting ? invitation : stage} onSubmit={submit}>
            {stage === "password" && <>
              <label htmlFor="merchant">{t("Merchant ID")}</label>
              <input id="merchant" value={merchant} onChange={event => setMerchant(event.target.value)} required />
              <label htmlFor="email">{t("Email")}</label>
              <input id="email" type="email" autoComplete="username" value={email}
                onChange={event => setEmail(event.target.value)} required />
            </>}
            {(stage === "password" || stage === "invite") && <>
              <label htmlFor="password">{accepting ? t("Create password") : t("Password")}</label>
              <input id="password" name="password" type="password" minLength={accepting ? 12 : undefined}
                autoComplete={accepting ? "new-password" : "current-password"} required />
              {accepting && <span className="small muted">{t("Use at least 12 characters and no more than 72 UTF-8 bytes.")}</span>}
            </>}
            {stage === "mfa" && <AuthenticatorCode />}
            {stage === "bootstrap" && <>
              <label htmlFor="session">{t("Local member session token")}</label>
              <input id="session" name="session" type="password" autoComplete="off" required />
            </>}
            {error && <p className="error" role="alert">{t(error)}</p>}
            <button className="primary full" disabled={busy}>{busy ? t("Please wait…") :
              { invite: t("Accept invitation"), bootstrap: t("Open workspace"), mfa: t("Verify"), password: t("Sign in") }[stage]}</button>
          </form>
        )}
        <div className="access-links">
          {stage === "password" ? <>
            <button className="text-button" type="button" disabled={busy}
              onClick={() => { setError(""); setBootstrap(true); }}>{t("Use local member session")}</button>
            <button className="text-button" type="button" disabled={busy} onClick={openSandbox}>{t("Use sandbox API key")}</button>
          </> : <button className="text-button" type="button" disabled={busy} onClick={backToSignIn}>{t("Back to sign-in")}</button>}
        </div>
      </section>
    </main>
  );
}

function AuthenticatorCode() {
  const { t } = useLocale();
  return <>
    <label htmlFor="code">{t("Authenticator code")}</label>
    <input id="code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}"
      maxLength={6} required />
  </>;
}

export function TeamSecurity({ token, signedOut, canEnroll }: {
  token: string;
  signedOut: (notice?: string) => void;
  canEnroll: boolean;
}) {
  const { t } = useLocale();
  const [security, setSecurity] = useState<SecurityStatus>();
  const [loadError, setLoadError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    request<SecurityStatus>(base + "/security", token, { signal: controller.signal }).then(result => {
      if (!controller.signal.aborted) setSecurity(result);
    }).catch(reason => {
      if (!controller.signal.aborted) setLoadError(message(reason));
    });
    return () => controller.abort();
  }, [token, version]);

  return (
    <section className="orders security-settings">
      <h2>{t("Two-factor authentication")}</h2>
      {loadError && <div className="workspace-feedback">
        <p role="alert" className="error">{t(loadError)}</p>
        <button className="secondary" onClick={() => {
          setLoadError(""); setVersion(value => value + 1);
        }}>{t("Retry security settings")}</button>
      </div>}
      {!security && !loadError && <p role="status" className="muted">{t("Loading security settings…")}</p>}
      {security && (security.mfa_enabled ? <p><span className="tag green">{t("MFA enabled")}</span></p> :
        canEnroll && security.password_set ? <Enrollment token={token}
          complete={() => signedOut("MFA enabled. Sign in again with your password and authenticator.")} /> :
          <p className="muted">{t(canEnroll ? "MFA setup is available to members who sign in with a password." : "Contact platform operations for authenticator recovery.")}</p>)}
    </section>
  );
}

function Enrollment({ token, complete }: { token: string; complete: () => void }) {
  const { t } = useLocale();
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const fields = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      if (secret) {
        await post("/security/mfa/confirm", { code: fields.get("code") }, token);
        setSecret("");
        complete();
      } else {
        const result = await post<{ secret: string }>("/security/mfa/setup", { password: fields.get("password") }, token);
        setSecret(result.secret);
      }
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form key={secret ? "confirm" : "setup"} className="payment-form" onSubmit={submit}>
      {secret ? <>
        <p className="muted">{t("Add this key to your authenticator as a time-based account, then enter its six-digit code.")}</p>
        <label htmlFor="setup-key">{t("Authenticator setup key")}</label>
        <input id="setup-key" readOnly value={secret} onFocus={event => event.currentTarget.select()} />
        <AuthenticatorCode />
      </> : <>
        <label htmlFor="current-password">{t("Current password")}</label>
        <input id="current-password" name="password" type="password" autoComplete="current-password" required />
      </>}
      {error && <p role="alert" className="error">{t(error)}</p>}
      <button className="primary" disabled={busy}>{secret ? t("Enable MFA") : t("Set up MFA")}</button>
      {secret && <button className="secondary" type="button" disabled={busy}
        onClick={() => { setSecret(""); setError(""); }}>{t("Start again")}</button>}
    </form>
  );
}

export function Invitations({ token, onBusy, invited }: {
  token: string; onBusy: (busy: boolean) => void; invited: () => void;
}) {
  const { t, dateTime, localizedLink } = useLocale();
  const [link, setLink] = useState("");
  const [credential, setCredential] = useState<{ email: string; initial_password?: string }>();
  const invitationKey = useRef("");
  const pendingInvitation = useRef<{ email: FormDataEntryValue | null; name: FormDataEntryValue | null; role: FormDataEntryValue | null } | undefined>(undefined);
  const [expires, setExpires] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const submitting = useRef(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting.current || link || credential || (uncertain && !memberPortal)) return;
    submitting.current = true;
    const form = event.currentTarget;
    const fields = new FormData(form);
    setBusy(true);
    onBusy(true);
    setError("");
    setLink("");
    try {
      const payload = { email: fields.get("invite-email"), name: fields.get("name"), role: fields.get("role") };
      if (memberPortal) {
        invitationKey.current ||= crypto.randomUUID();
        pendingInvitation.current ??= payload;
        setCredential(await request<{ email: string; initial_password?: string }>(base + "/invitations", token, {
          method: "POST", body: JSON.stringify(pendingInvitation.current), headers: { "Idempotency-Key": invitationKey.current },
        }));
        invited();
      } else {
        const result = await post<{ invite_path: string; expires_at: string }>("/invitations", payload, token);
        setLink(window.location.origin + result.invite_path);
        setExpires(result.expires_at);
      }
      form.reset();
    } catch (reason) {
      if (!(reason instanceof ApiError) || reason.status >= 500) {
        setUncertain(true);
        setError(memberPortal
          ? "The invitation result is unknown. Retry the same invitation to check its status."
          : "The invitation result is unknown. Only a returned link can be shared. An unused link expires after 24 hours.");
      } else {
        if (!uncertain) {
          invitationKey.current = "";
          pendingInvitation.current = undefined;
        }
        setError(message(reason));
      }
    } finally {
      submitting.current = false;
      setBusy(false);
      onBusy(false);
    }
  }

  return (
    <>
      <p className="muted drawer-intro">{t(memberPortal
        ? "Share the initial password securely with the member. They must set a new password and bind an authenticator within 24 hours."
        : "Generate a local link using a test email address. No email is sent.")}</p>
      <form className="payment-form" onSubmit={submit}>
        <fieldset className="workspace-fields" disabled={busy || Boolean(link) || Boolean(credential) || uncertain}>
          <label htmlFor="invite-name">{t("Name")}</label>
          <input id="invite-name" name="name" maxLength={100} required />
          <label htmlFor="invite-email">{t(memberPortal ? "Email" : "Sandbox email")}</label>
          <input id="invite-email" name="invite-email" type="email" maxLength={254} required />
          <label htmlFor="role">{t("Role")}</label>
          <select id="role" name="role"><option value="viewer">{t("Viewer")}</option><option value="operator">{t("Operator")}</option><option value="admin">{t("Admin")}</option></select>
          <button className="primary">{t(memberPortal ? "Create invitation" : "Generate invitation link")}</button>
        </fieldset>
        {error && <p role="alert" className="error">{t(error)}</p>}
        {memberPortal && uncertain && !credential && <button className="secondary" disabled={busy}>{t("Check invitation")}</button>}
        {credential && (credential.initial_password ? <>
          <label htmlFor="member-initial">{t("Initial password")}</label>
          <input id="member-initial" readOnly value={credential.initial_password} onFocus={event => event.currentTarget.select()} />
          <p className="muted small">{credential.email} · <a href={localizedLink(memberLogin)}>{t("Merchant sign-in")}</a></p>
        </> : <p role="status">{t("This invitation already exists. The initial password was shown only when it was created.")}</p>)}
        {link && <>
          <label htmlFor="invite-link">{t("Copy sandbox invitation link")}</label>
          <input id="invite-link" readOnly value={link} onFocus={event => event.currentTarget.select()} />
          <span className="muted small">{t("Expires")} {dateTime(expires)}</span>
        </>}
      </form>
    </>
  );
}
