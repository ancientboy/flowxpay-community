import { useEffect } from "react";
import { LanguageSelect, useLocale } from "../locale";
import { MemberWorkspace } from "../MemberWorkspace";
import { PlatformConsole } from "../PlatformWorkspace";
import { sandboxCapabilities } from "../memberEnvironment";
import type { MemberSession } from "../WorkspaceAccess";
import fixtures from "./fixtures.json";
import "./demo.css";

const token = "community-read-only";
const demoMember = fixtures["/sandbox/workspace/session"] as MemberSession;
const capabilities = { ...sandboxCapabilities, createSites: false, teamManagement: false, mfaEnrollment: false };

function brand() {
  return <div className="brand-wrap"><strong className="community-wordmark">FlowXPay</strong></div>;
}

export function App() {
  const { locale, t } = useLocale();
  const zh = locale === "zh-CN";
  const params = new URLSearchParams(window.location.search);
  const view = params.get("workspace");
  const title = view === "operator" ? t("Platform operations") : view === "merchant" ? t("Merchant workspace")
    : zh ? "社区版在线演示" : "Community demo";
  const url = (workspace?: string) => "?" + new URLSearchParams({ lang: locale, ...(workspace && { workspace }) });
  const operator = url("operator");
  const merchant = url("merchant");
  const simulation = "./simulation/?lang=" + (zh ? "zh" : "en");
  const home = () => window.location.assign(url());

  useEffect(() => { document.title = title + " · FlowXPay"; }, [title]);

  const demoLinks = <nav aria-label={zh ? "演示入口" : "Demo workspaces"}>
        <a href={operator} aria-current={view === "operator" ? "page" : undefined}>{t("Platform operations")}</a>
        <a href={merchant} aria-current={view === "merchant" ? "page" : undefined}>{t("Merchant workspace")}</a>
        <a href={simulation}>{zh ? "模拟支付" : "Payment simulation"}</a>
      </nav>;

  return <>
    <header className="community-demo-bar">
      <a href={url()} className="community-wordmark">FlowXPay</a>
      {demoLinks}
      <span>{zh ? "只读演示 · 示例数据" : "Read-only demo · sample data"}</span>
      {!view && <LanguageSelect />}
    </header>
    {view === "operator" ? <PlatformConsole demoLinks={demoLinks} initialPage={params.get("page") === "collections" ? "Payments" : params.get("page") === "payouts" ? "Payouts" : "Overview"} logo={brand()} session={{ token, role: "viewer", user: "Demo platform operator" }} signOut={home} />
      : view === "merchant" ? <MemberWorkspace demoLinks={demoLinks} logo={brand()} capabilities={capabilities} token={token} member={demoMember} signedOut={home} />
      : <main className="community-demo-home">
        <h1>{title}</h1>
        <p className="muted">{zh ? "进入社区版后台，查看示例商户、上游通道、订单、费率和对账。完整操作可在本地沙盒使用。"
          : "Explore sample merchants, channels, orders, pricing and reconciliation in the community workspaces. Use the local sandbox for write operations."}</p>
        <div className="community-demo-options">
          <article className="platform-card">
            <h2>{t("Platform operations")}</h2>
            <p>{zh ? "查看商户、通道配置、代收代付订单和平台经营数据。" : "Browse merchants, channel configuration, collection and payout orders, and platform reports."}</p>
            <a className="primary" href={operator}>{zh ? "进入运营后台" : "Open platform operations"}</a>
          </article>
          <article className="platform-card">
            <h2>{t("Merchant workspace")}</h2>
            <p>{zh ? "查看商户订单、获配通道、费率、对账和 API 接入文档。" : "Browse merchant orders, assigned channels, rates, reconciliation, and API documentation."}</p>
            <a className="primary" href={merchant}>{zh ? "进入商户后台" : "Open merchant workspace"}</a>
          </article>
        </div>
        <p className="community-demo-docs"><a href="https://github.com/ancientboy/flowxpay-community">{zh ? "获取源码与一键部署文档" : "Source code and self-hosting guide"}</a></p>
      </main>}
  </>;
}
