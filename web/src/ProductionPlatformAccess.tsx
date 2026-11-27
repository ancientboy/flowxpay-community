import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError, request } from "./api";
import { LanguageSelect, useLocale } from "./locale";
import { platformAPI, productionMerchantLogin } from "./platformEnvironment";
import type { PlatformSession } from "./PlatformWorkspace";

type Identity = { email: string; role: "operator" | "viewer" | "merchant"; platform_owner?: boolean };
const errors: Record<string, string> = {
  invalid_login: "Check your email, password and authenticator code.",
  login_busy: "Too many sign-in attempts. Try again shortly.",
  invalid_setup: "Check the setup fields and try again.",
  setup_denied: "The first-time setup could not be authorized.",
  setup_code_invalid: "Check the code from your new authenticator.",
};

function setupKey() {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let buffer = 0;
  let bits = 0;
  let key = "";
  for (const byte of crypto.getRandomValues(new Uint8Array(20))) {
    buffer = (buffer << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      key += alphabet[(buffer >> bits) & 31];
    }
  }
  return key;
}

function PasswordField({ id, label, initial = false, fresh = false }: {
  id: string; label: string; initial?: boolean; fresh?: boolean;
}) {
  const { t } = useLocale();
  const [visible, setVisible] = useState(false);
  return <>
    <label htmlFor={id}>{t(label)}</label>
    <input id={id} name={id} type={visible ? "text" : "password"} required
      minLength={fresh ? 16 : undefined} maxLength={128}
      autoComplete={initial ? "off" : fresh ? "new-password" : "current-password"} />
    <button type="button" className="text-button" data-password-toggle={id}
      onClick={() => setVisible(!visible)}>{t(visible ? "Hide password" : "Show password")}</button>
  </>;
}

export function ProductionPlatformAccess({ logo, onSession, notice }: {
  logo: ReactNode; onSession: (session: PlatformSession) => void; notice: string;
}) {
  const { t, localizedLink } = useLocale();
  const [seed, setSeed] = useState("");
  const [preparing, setPreparing] = useState(true);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const merchantURL = localizedLink("/flowxpay/merchant");
  const opsURL = localizedLink("/flowxpay/ops/");

  function enter(identity: Identity) {
    if (identity.role === "merchant") {
      location.replace(merchantURL);
    } else if (productionMerchantLogin) {
      location.replace(opsURL);
    } else {
      onSession({ user: identity.email, role: identity.role, token: "", platformOwner: identity.platform_owner });
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    request<Identity>(platformAPI + "/session", undefined, { signal: controller.signal }).then(identity => {
      if (!controller.signal.aborted) enter(identity);
    }).catch(reason => {
      if (!controller.signal.aborted && !(reason instanceof ApiError && reason.status === 401)) {
        setError("Could not check your session.");
      }
    }).finally(() => { if (!controller.signal.aborted) setPreparing(false); });
    return () => controller.abort();
  }, [merchantURL, opsURL, onSession]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    const fields = new FormData(event.currentTarget);
    const code = String(fields.get("code")).replace(/\s/g, "");
    const setup = Boolean(seed);
    const password = fields.get("new-password");
    if (setup && password !== fields.get("confirm-password")) {
      setError("The passwords must match.");
      return;
    }
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      await request(platformAPI + (setup ? "/setup" : "/login"), undefined, {
        method: "POST",
        body: JSON.stringify(setup ? {
          email: fields.get("email"), initial_password: fields.get("initial-password"), new_password: password,
          totp_seed: seed, code,
        } : { email: fields.get("email"), password: fields.get("password"), code }),
      });
      if (setup) {
        setSeed("");
        setSuccess("Password and authenticator saved. Wait for the next code before signing in.");
      } else {
        enter(await request<Identity>(platformAPI + "/session"));
      }
    } catch (reason) {
      setError(reason instanceof ApiError ? errors[reason.code ?? ""] ?? reason.message : "The request could not be completed.");
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  const heading = seed ? "First-time setup" : productionMerchantLogin ? "Merchant sign-in" : "Operator sign-in";
  return <main className="connect-shell"><section className="connect">
    <div className="access-header">{logo}<LanguageSelect /></div>
    <span className="tag blue">{t("Production")}</span>
    <h1 id="signin-heading">{t(heading)}</h1>
    {preparing ? <p role="status">{t("Loading…")}</p> : <form key={heading} onSubmit={submit}>
      <label htmlFor={seed ? "setup-email" : "email"}>{t("Email")}</label>
      <input id={seed ? "setup-email" : "email"} name="email" type="email" autoComplete="username" required maxLength={254} />
      {seed ? <>
        <PasswordField id="initial-password" label="Initial password" initial />
        <PasswordField id="new-password" label="New password" fresh />
        <PasswordField id="confirm-password" label="Confirm new password" fresh />
        <label htmlFor="setup-key">{t("Authenticator setup key")}</label>
        <input id="setup-key" readOnly autoComplete="off" spellCheck={false} value={seed} onFocus={e => e.currentTarget.select()} />
        <p className="muted small">{t("Enter this key in your authenticator as a time-based code using SHA-1, six digits and a 30-second period.")}</p>
      </> : <PasswordField id="password" label="Password" />}
      <label htmlFor={seed ? "setup-code" : "code"}>{t("Authenticator code")}</label>
      <input id={seed ? "setup-code" : "code"} name="code" inputMode="numeric" autoComplete="one-time-code" required
        maxLength={10} pattern="[0-9 ]{6,10}" />
      {(error || notice) && <p role="alert" className="error">{t(error || notice)}</p>}
      {success && <p id="notice" role="status">{t(success)}</p>}
      <button className="primary full" disabled={busy}>{t(busy ? "Please wait…" : seed ? "Save password and bind authenticator" : "Sign in")}</button>
    </form>}
    <div className="access-links">
      {!preparing && <button className="text-button" type="button" disabled={busy} onClick={() => {
        setError(""); setSuccess(""); setSeed(seed ? "" : setupKey());
      }}>{t(seed ? "Back to sign-in" : "First-time setup")}</button>}
      <a className="text-button" id="portal-link" href={productionMerchantLogin ? opsURL : localizedLink("/flowxpay/ops/?portal=merchant")}>
        {t(productionMerchantLogin ? "Operator sign-in" : "Merchant sign-in")}
      </a>
    </div>
  </section></main>;
}
