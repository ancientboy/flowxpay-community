import { useState } from "react";
import { ArrowRight, CheckCircle, CreditCard, QrCode } from "@phosphor-icons/react";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";

const demos = [
  { name: "Thailand PromptPay", market: "Thailand", currency: "THB", method: "Scan with PromptPay", amount: 32000, qr: true },
  { name: "Brazil Pix", market: "Brazil", currency: "BRL", method: "Pay by Pix", amount: 12900, qr: true },
  { name: "India gateway", market: "India", currency: "INR", method: "India gateway", amount: 24900, qr: false },
  { name: "US card", market: "United States", currency: "USD", method: "Pay by card", amount: 4900, qr: false },
] as const;

export function PaymentDemos() {
  const { t, money } = useLocale();
  const [demo, setDemo] = useState<typeof demos[number]>();
  const [stage, setStage] = useState<"checkout" | "gateway" | "success">("checkout");
  return <div className="payment-demos">
    <p className="muted">{t("Explore sample checkout flows. Demos do not create orders, charge funds or change channel access.")}</p>
    <div className="payment-demo-grid">
      {demos.map(item => <section key={item.name} className="workspace-panel payment-demo-card">
        <div className="payment-demo-label"><span>{t(item.market)}</span><span className="tag blue">{item.currency}</span></div>
        <h2>{t(item.name)}</h2>
        <p className="muted">{t(item.qr ? "QR checkout" : "Hosted checkout with gateway redirect")}</p>
        <button type="button" className="secondary" aria-label={`${t("Preview checkout")} · ${t(item.name)}`}
          onClick={() => { setStage("checkout"); setDemo(item); }}>
          {t("Preview checkout")} <ArrowRight size={16} aria-hidden="true" />
        </button>
      </section>)}
    </div>
    {demo && <Dialog title={t("Checkout demo")} close={() => setDemo(undefined)}>
      <div className="payment-demo-checkout">
        <header><strong className="community-wordmark">FlowXPay</strong><span className="tag blue">{t("Sandbox")}</span></header>
        <h3>{t(stage === "success" ? "Payment confirmed" : stage === "gateway" ? "Mock payment gateway" : demo.method)}</h3>
        <strong className="checkout-amount">{money(demo.amount, demo.currency)}</strong>
        <p className="muted">{t("This is a simulation. You will not be charged.")}</p>
        {stage === "success" ? <>
          <div className="checkout-symbol success"><CheckCircle size={44} aria-hidden="true" /></div>
          <p role="status">{t("The sandbox payment is complete. You can close this page.")}</p>
          <button type="button" className="secondary full" onClick={() => setStage("checkout")}>{t("Restart demo")}</button>
        </> : <>
          {demo.qr ? <>
            <QrCode size={96} aria-hidden="true" />
            <p className="muted small">{t("Sample QR only. No payment details are encoded.")}</p>
          </> : <div className="checkout-symbol"><CreditCard size={38} aria-hidden="true" /></div>}
          {demo.qr || stage === "gateway" ?
            <button type="button" className="primary full" onClick={() => setStage("success")}>
              {demo.qr && <QrCode size={18} aria-hidden="true" />}{t("Simulate payment")}
            </button> :
            <button type="button" className="primary full" onClick={() => setStage("gateway")}>{t("Continue to mock gateway")}</button>}
        </>}
      </div>
    </Dialog>}
  </div>;
}
