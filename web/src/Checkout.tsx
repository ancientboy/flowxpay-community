import { LanguageSelect, useLocale } from "./locale";
import { useEffect, useRef, useState } from "react";
import { CheckCircle, CreditCard } from "@phosphor-icons/react";
import { ApiError, type Checkout as CheckoutData, request } from "./api";

export function Checkout({ token, handoff }: { token: string; handoff: boolean }) {
  const { t, money, localizedLink } = useLocale();
  const checkoutPath = `/checkout/${token}`;
  const endpoint = `/sandbox/checkout/${encodeURIComponent(token)}`;
  const [data, setData] = useState<CheckoutData>();
  const [readError, setReadError] = useState<"expired" | "missing" | "retry">();
  const [confirmationUnknown, setConfirmationUnknown] = useState(false);
  const [revision, setRevision] = useState(0);
  const [checking, setChecking] = useState(true);
  const [confirming, setConfirming] = useState(false);
  const confirmPending = useRef(false);
  const [now, setNow] = useState(Date.now);

  useEffect(() => {
    const controller = new AbortController();
    request<CheckoutData>(endpoint, undefined, { signal: controller.signal })
      .then(result => {
        if (controller.signal.aborted) return;
        setData(result);
        setReadError(undefined);
        setConfirmationUnknown(false);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        const status = reason instanceof ApiError ? reason.status : 0;
        setReadError(status === 410 ? "expired" : status === 404 ? "missing" : "retry");
      })
      .finally(() => { if (!controller.signal.aborted) setChecking(false); });
    return () => controller.abort();
  }, [endpoint, revision]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  function refresh() {
    setChecking(true);
    setRevision(value => value + 1);
  }

  async function confirm() {
    if (confirmPending.current) return;
    confirmPending.current = true;
    setConfirming(true);
    try {
      setData(await request<CheckoutData>(`${endpoint}/confirm`, undefined, {
        method: "POST", body: JSON.stringify({ confirm: true }),
      }));
    } catch {
      setConfirmationUnknown(true);
    } finally {
      setConfirming(false);
      confirmPending.current = false;
    }
  }

  const next = data?.next_action;
  const qr = next?.type === "display_qr" && next.qr_image_url === endpoint + "/qr" ? next.qr_image_url : undefined;
  const redirect = next?.type === "redirect" && next.url === checkoutPath + "/pay" ? next.url : undefined;
  const legacyCheckout = next?.type === "redirect" && next.url === checkoutPath;
  const isPaid = data?.status === "succeeded";
  const isProcessing = data?.status === "pending_confirmation";
  const seconds = data ? Math.max(0, Math.ceil((new Date(data.expires_at).getTime() - now) / 1000)) : 0;
  const windowClosed = readError === "expired" || (data !== undefined && seconds === 0);
  const isExpired = data?.status === "expired" ||
    (windowClosed && !isPaid && !isProcessing && !confirmationUnknown && data?.status !== "failed");
  const canPay = data?.status === "requires_action" && seconds > 0 && !readError && !confirmationUnknown &&
    Boolean(qr || redirect || legacyCheckout);
  const methodTitle = data?.method === "pix" ? "Pay by Pix" : data?.method === "gateway" ? "India gateway" : "Pay by card";
  const title = isPaid ? "Payment confirmed" : isExpired ? "Payment link expired"
    : isProcessing || confirmationUnknown ? "Payment pending confirmation"
    : data?.status === "failed" ? "Payment unsuccessful"
    : canPay ? (handoff && redirect ? "Mock payment gateway" : methodTitle) : "Checkout unavailable";
  const showRefresh = !isPaid && !isExpired && !windowClosed &&
    (isProcessing || confirmationUnknown || readError === "retry");
  const reference = data ? t("Keep order {reference} for your records.", { reference: data.reference }) : "";

  return <main className="checkout-shell">
    <section className="checkout" aria-label={t("FlowXPay checkout")}>
      <header><strong className="community-wordmark">FlowXPay</strong><div className="checkout-tools"><span className="tag blue">{t("Sandbox")}</span><LanguageSelect /></div></header>
      {!data && checking && !readError ? <p role="status">{t("Loading checkout…")}</p> : <>
        {data && <>
          <p className="merchant-label">{data.merchant_name}</p>
          <p className="muted checkout-reference">{t("Order {reference}", { reference: data.reference })}</p>
        </>}
        <h1>{t(title)}</h1>
        {data && <strong className="checkout-amount">{money(data.amount_minor, data.currency)}</strong>}
        {canPay && <>
          {qr ? <img className="checkout-qr" alt={data.method === "pix" ? t("Simulated Pix QR code") : t("Simulated India gateway QR code")} src={qr} width="192" height="192" />
            : <div className="checkout-symbol"><CreditCard size={40} aria-hidden="true" /></div>}
          <p className="muted">{t("This is a simulation. You will not be charged.")}</p>
          <p className="muted">{t("Expires in {time}", { time: `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}` })}</p>
          {redirect && !handoff ?
            <a className="primary full" href={localizedLink(redirect)}>{t("Continue to mock gateway")}</a> :
            <button className="primary full" onClick={confirm} disabled={confirming || checking}>{t(confirming ? "Confirming…" : "Simulate payment")}</button>}
        </>}
        {isPaid && <>
          <div className="checkout-symbol success"><CheckCircle size={48} aria-hidden="true" /></div>
          <p className="muted" role="status">{t("The sandbox payment is complete. You can close this page.")}</p>
          <p className="muted wrap">{reference}</p>
        </>}
        {isExpired && <p className="muted checkout-next" role="status">{t("Ask the merchant for a new payment link.")}</p>}
        {!canPay && !isPaid && !isExpired && <div className="checkout-next">
          <p className="muted" role="status">{t(
            windowClosed && (isProcessing || confirmationUnknown)
              ? "The checkout window has closed. Ask the merchant to confirm the order status before trying again."
              : isProcessing || confirmationUnknown
                ? "The payment result is pending. Refresh the status before trying again."
                : readError === "retry"
                  ? "The payment status could not be loaded. Refresh the status to try again."
                  : data?.status === "failed"
                    ? "Ask the merchant to check this order and send a new link if needed."
                    : "Check the payment link with the merchant.",
          )}</p>
          {next?.type === "unsupported" && !windowClosed && <p className="muted">
            {t("The simulated gateway returned an unsupported action. Ask the merchant to check this order.")}
          </p>}
          {readError === "retry" && (isProcessing || confirmationUnknown) && <p className="error" role="alert">
            {t("Unable to check this session.")}
          </p>}
        </div>}
        {showRefresh && <button className="primary full" disabled={checking || confirming} onClick={refresh}>
          {t(checking ? "Checking status…" : "Refresh payment status")}
        </button>}
      </>}
    </section>
  </main>;
}
