import { useLocale } from "./locale";
import { useEffect, useState, type FormEvent } from "react";
import { parseMinorUnits, request, type CollectionException, type Payment, type Refund } from "./api";

export const collectionStatuses:Record<string,string> = { review: "Payment review pending", draft: "Draft", rejected: "Rejected", processing: "Processing", queued:"Queued", routing:"Processing", cancelled:"Cancelled", pending_approval:"Pending approval", requires_action: "Awaiting payment", succeeded: "Paid", expired: "Expired", pending_confirmation: "Pending confirmation", failed: "Failed" };

export function PaymentStatusBadge({ payment, payout }: {
  payment: { status: string; checkout_needs_review?: boolean }; payout?: boolean;
}) {
  const { t } = useLocale();
  if (payment.checkout_needs_review && !payout) return <span className="tag amber"><i aria-hidden="true" />{t("Payment review pending")}</span>;
  const variant = payment.status === "succeeded" ? "green" : payment.status === "expired" ? "gray" : payment.status === "failed" ? "red" : "amber";
  return <span className={`tag ${variant}`}><i aria-hidden="true" />{t(payout && payment.status === "succeeded" ? "Succeeded" : collectionStatuses[payment.status] ?? payment.status)}</span>;
}

const refundStates = { succeeded: "Refunded", failed: "Failed", pending_confirmation: "Pending confirmation" };
const exceptionNames: Record<CollectionException["kind"], string> = {
  terminal_conflict: "Conflicting channel status",
  replay_conflict: "Channel event ID conflict",
  webhook_failed: "Webhook delivery failed",
  payment_confirmation_pending: "Payment confirmation pending",
  refund_confirmation_pending: "Refund confirmation pending",
};

export function Refunds({ keyValue, payment, signedWrites }: { keyValue: string; payment: Payment; signedWrites?: boolean }) {
  const { t, money } = useLocale();
  const path = `/v1/payments/${encodeURIComponent(payment.id)}/refunds`;
  const [refunds, setRefunds] = useState<Refund[]>([]);
  const [reference, setReference] = useState("");
  const [amount, setAmount] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  async function reload() {
    setError("");
    setLoading(true);
    try { setRefunds((await request<{ data: Refund[] }>(path, keyValue)).data); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Unable to load refunds."); }
    finally { setLoading(false); }
  }
  useEffect(() => { void reload(); }, [path, keyValue]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    const minor = parseMinorUnits(amount);
    if (minor === undefined) {
      setError("Enter an amount with up to two decimal places.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const refund = await request<Refund>(path, keyValue, {
        method: "POST", headers: { "Idempotency-Key": reference.trim() }, body: JSON.stringify({ amount_minor: minor }),
      });
      setRefunds(current => [refund, ...current.filter(item => item.id !== refund.id)].slice(0, 100));
      setReference("");
      setAmount("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to verify the request. Retry with the same refund request ID and amount.");
    } finally {
      setBusy(false);
    }
  }

  return <section aria-label={t("Refunds")} className="payment-detail">
    <div className="detail-actions">
      <h3>{t("Refunds")}</h3><button className="secondary" onClick={() => void reload()} disabled={busy || loading}>{t("Refresh refunds")}</button>
    </div>
    {loading ? <p role="status" className="muted">{t("Loading refunds…")}</p> : refunds.length === 0 && <p className="muted">{t("No refunds recorded.")}</p>}
    {refunds.length > 0 && <div className="table-scroll" tabIndex={0} role="region" aria-label={t("Refunds")}><table className="refund-table">
      <thead><tr><th scope="col">{t("Amount")}</th><th scope="col">{t("Status")}</th><th scope="col">{t("Refund ID")}</th></tr></thead>
      <tbody>{refunds.map(refund => <tr key={refund.id}>
        <td>{money(refund.amount_minor, refund.currency)}</td><td>{t(refundStates[refund.status])}</td><td className="wrap">{refund.id}</td>
      </tr>)}</tbody>
    </table></div>}
    {signedWrites ? <p className="muted">{t("Submit refunds from your server using a signed request.")}</p> :
      <form className="payment-form" onSubmit={submit}>
      <label htmlFor="refund-reference">{t("Refund request ID")}</label>
      <input id="refund-reference" value={reference} onChange={event => setReference(event.target.value)}
        pattern="[a-zA-Z0-9._:\-]+" maxLength={80} required disabled={busy} aria-describedby="refund-retry" />
      <p id="refund-retry" className="muted small">{t("If a request is interrupted, reuse the same request ID and amount.")}</p>
      <label htmlFor="refund-amount">{t("Refund amount ({currency})", { currency: payment.currency })}</label>
      <input id="refund-amount" value={amount} onChange={event => setAmount(event.target.value)} inputMode="decimal" required disabled={busy} />
      {error && <p className="error" role="alert">{t(error)}</p>}
      <button className="secondary" disabled={busy || loading}>{busy ? t("Submitting…") : t("Create sandbox refund")}</button>
      </form>}
  </section>;
}

export function CollectionExceptions({ keyValue, open }: { keyValue: string; open: (id: string) => void }) {
  const { t, dateTime } = useLocale();
  const [items, setItems] = useState<CollectionException[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  async function reload() {
    setLoading(true);
    setError("");
    try { setItems((await request<{ data: CollectionException[] }>("/v1/collection-exceptions", keyValue)).data); }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Unable to load collection exceptions."); }
    finally { setLoading(false); }
  }
  useEffect(() => { void reload(); }, [keyValue]);

  return <section className="orders" aria-label={t("Collection exceptions")}>
    <div className="detail-actions">
      <button className="secondary" onClick={() => void reload()} disabled={loading}>{t("Refresh exceptions")}</button>
    </div>
    {error && <p className="error" role="alert">{t(error)}</p>}
    {loading ? <p role="status" className="muted">{t("Loading exceptions…")}</p> :
      !error && items.length === 0 && <p className="muted">{t("No collection exceptions.")}</p>}
    {items.length > 0 && <div className="table-scroll" tabIndex={0} role="region" aria-label={t("Collection exceptions")}><table className="exception-table">
      <thead><tr><th scope="col">{t("Exception")}</th><th scope="col">{t("Payment")}</th><th scope="col">{t("Related ID")}</th><th scope="col">{t("Recorded")}</th></tr></thead>
      <tbody>{items.map(item => <tr key={JSON.stringify(item)}>
        <td>{t(exceptionNames[item.kind])}</td>
        <td><button className="text-button" onClick={() => open(item.payment_id)}>{item.payment_id}</button></td>
        <td>{item.event_id ?? item.subject_id}</td>
        <td>{dateTime(item.at)}</td>
      </tr>)}</tbody>
    </table></div>}
  </section>;
}
