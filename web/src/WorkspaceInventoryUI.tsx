import { useLocale } from "./locale";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ApiError, request } from "./api";
import { Dialog } from "./Dialog";
import { message } from "./WorkspaceAccess";

export function useWorkspaceData<T>(path: string | undefined, token: string, load = request<T>) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    load(path, token, { signal: controller.signal }).then(value => {
      if (!controller.signal.aborted) setData(value);
    }).catch(reason => {
      if (!controller.signal.aborted) setError(reason);
    });
    return () => controller.abort();
  }, [path, token, revision, load]);
  const refresh = () => {
    setError(undefined);
    setRevision(value => value + 1);
  };
  return { data, error, refresh, reload: () => {
    setData(undefined);
    refresh();
  } };
}

export function LoadState<T>({ state, label, children }: {
  state: ReturnType<typeof useWorkspaceData<T>>;
  label: string;
  children: (data: T) => ReactNode;
}) {
  const { t } = useLocale();
  if (state.error instanceof ApiError && state.error.status === 403) {
    return <p className="muted empty" role="status">{t("Your current role does not have access to {resource}.", { resource: label })}</p>;
  }
  if (state.error) return <div className="workspace-feedback">
    <p className="error" role="alert">{t("Could not load {resource}.", { resource: label })} {t(message(state.error))}</p>
    <button className="secondary" onClick={state.reload}>{t("Retry")}</button>
  </div>;
  if (!state.data) return <p className="muted empty" role="status">{t("Loading {resource}…", { resource: label })}</p>;
  return children(state.data);
}

export function WorkspaceTable({ label, columns, children }: { label: string; columns: string[]; children: ReactNode }) {
  const { t } = useLocale();
  return <div className="table-scroll" tabIndex={0} role="region" aria-label={t("{table} scroll area", { table: label })}><table aria-label={label}>
    <thead><tr>{columns.map(column => <th key={column} scope="col">{column}</th>)}</tr></thead>
    <tbody>{children}</tbody>
  </table></div>;
}

export function Details({ fields }: { fields: [string, ReactNode][] }) {
  return <div className="payment-detail"><dl>{fields.map(([label, value]) =>
    <div className="detail-field" key={label}><dt>{label}</dt><dd className="wrap">{value}</dd></div>,
  )}</dl></div>;
}

export function InventoryForm<T = unknown>({ token, path, title, close, created, unavailable, children, payload, submitLabel = "Create", onboardingAuthenticator = false }: {
  token: string; path: string; title: string; close: () => void; created: (result?: T) => void;
  unavailable?: boolean; children: ReactNode; submitLabel?: string;
  payload?: (data: FormData) => object; onboardingAuthenticator?: boolean;
}) {
  const { t } = useLocale();
  const pending = useRef(false);
  const attempt = useRef<{ key: string; body: string }>(undefined);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState("");
  const [needsAuthenticator, setNeedsAuthenticator] = useState(false);
  const [authenticatorCode, setAuthenticatorCode] = useState("");
  const authenticatorInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (needsAuthenticator && !busy && !uncertain) authenticatorInput.current?.focus();
  }, [needsAuthenticator, busy, uncertain]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || unavailable) return;
    const fields = new FormData(event.currentTarget);
    const body = JSON.stringify(payload ? payload(fields) : Object.fromEntries(fields));
    const submission = attempt.current && (uncertain || attempt.current.body === body)
      ? attempt.current : { key: crypto.randomUUID(), body };
    attempt.current = submission;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      const headers = new Headers({ "Idempotency-Key": submission.key });
      if (needsAuthenticator) headers.set("X-FlowXPay-Onboarding-Code", authenticatorCode.replace(/\s/g, ""));
      const result = await request<T>(path, token, { method: "POST", headers, body: submission.body });
      created(result);
    } catch (reason) {
      const authRequired = onboardingAuthenticator && reason instanceof ApiError &&
        ["onboarding_authenticator_required", "onboarding_authenticator_invalid", "onboarding_authenticator_locked"].includes(reason.code ?? "");
      if (authRequired) {
        setNeedsAuthenticator(true);
        setAuthenticatorCode("");
        setUncertain(false);
        setError(reason.code === "onboarding_authenticator_required" ? "" : reason.message);
      } else if (uncertain || !(reason instanceof ApiError) || reason.status >= 500) {
        setUncertain(true);
        setError(message(reason));
      } else {
        attempt.current = undefined;
        setError(message(reason));
      }
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }

  return <Dialog drawer title={title} locked={busy || uncertain} close={close}>
    <form className="payment-form" onSubmit={submit}>
      <fieldset className="workspace-fields" disabled={busy || uncertain}>{children}</fieldset>
      {needsAuthenticator && <div className="workspace-fields">
        <label htmlFor="onboarding-authenticator-code">{t("Authenticator code")}</label>
        <p className="muted small" id="onboarding-authenticator-help">{t("Verify this request with your authenticator to continue opening the merchant account.")}</p>
        <input id="onboarding-authenticator-code" ref={authenticatorInput} value={authenticatorCode}
          onChange={event => setAuthenticatorCode(event.target.value)} inputMode="numeric" autoComplete="one-time-code"
          maxLength={10} pattern="[0-9 ]{6,10}" required={!uncertain} disabled={busy || uncertain}
          aria-describedby="onboarding-authenticator-help" />
      </div>}
      {error && <p role="alert" className="error">{uncertain && t("Creation could not be confirmed. Retry the same request.") + " "}{t(error)}</p>}
      <button className="primary" disabled={busy || unavailable}>
        {busy ? t("Creating…") : uncertain ? t("Retry same request") : t(needsAuthenticator ? "Verify and open account" : submitLabel)}
      </button>
      {uncertain && <button type="button" className="secondary" disabled={busy} onClick={() => created()}>{t("Close and reload list")}</button>}
    </form>
  </Dialog>;
}
