import { useEffect, useState, type ReactNode } from "react";
import { Moon, SidebarSimple, SignOut, Sun, Translate, UserCircle } from "@phosphor-icons/react";
import { Dialog } from "./Dialog";
import { PayoutFilterSelect } from "./PayoutFilterSelect";
import { languages, resolveLocale, useLocale } from "./locale";
import "./platform-payout-orders.css";

export function ConsoleShell({ workspace, title, accountLabel, accountName, logo, header, environment,
  navigation, orderPage = false, signingOut, logout, children }: {
  workspace: "platform" | "merchant"; title: string; accountLabel: string; accountName: string;
  logo: ReactNode; header: ReactNode; environment: string; navigation: ReactNode;
  orderPage?: boolean; signingOut: boolean; logout: () => Promise<void>; children: ReactNode;
}) {
  const { t, locale, setLocale } = useLocale();
  const navID = workspace + "-navigation";
  const sidebarKey = "flowxpay." + workspace + ".sidebar.collapsed";
  const platformThemeKey = "flowxpay.platform.payouts.theme";
  const [darkTheme, setDarkTheme] = useState(() => {
    const theme = new URLSearchParams(location.search).get("theme");
    if (theme === "light" || theme === "dark") return theme === "dark";
    try { return localStorage.getItem(platformThemeKey) === "dark"; }
    catch { return false; }
  });

  function toggleTheme() {
    const next = !darkTheme;
    setDarkTheme(next);
    const url = new URL(location.href);
    if (url.searchParams.has("theme")) {
      url.searchParams.delete("theme");
      history.replaceState(history.state, "", url);
    }
    try { localStorage.setItem(platformThemeKey, next ? "dark" : "light"); }
    catch { /* The selection still applies when browser storage is unavailable. */ }
  }

  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem(sidebarKey) === "true"; }
    catch { return false; }
  });
  const [narrow, setNarrow] = useState(() => window.matchMedia("(max-width: 680px)").matches);
  const [mobileMenu, setMobileMenu] = useState(false);
  useEffect(() => {
    const query = window.matchMedia("(max-width: 680px)");
    const resize = () => { setNarrow(query.matches); setMobileMenu(false); };
    query.addEventListener("change", resize);
    return () => query.removeEventListener("change", resize);
  }, []);
  const expanded = narrow ? mobileMenu : !sidebarCollapsed;
  const sidebarLabel = t(expanded ? "Collapse navigation" : "Expand navigation");

  function toggleSidebar() {
    if (narrow) { setMobileMenu(!mobileMenu); return; }
    const next = !sidebarCollapsed;
    setSidebarCollapsed(next);
    try { localStorage.setItem(sidebarKey, String(next)); }
    catch { /* Keep the current selection in this session. */ }
  }

  const themeLabel = locale === "zh-CN" ? "深色模式" : "Dark mode";
  const sidebar = <>
    <nav id={navID} onClick={event => { if (event.target instanceof Element && event.target.closest("button")) setMobileMenu(false); }} className="nav" aria-label={title}>
      {navigation}
    </nav>
    <div className="operator-account" role="group" aria-label={accountLabel}>
      <div className="operator-profile"><UserCircle size={24} aria-hidden="true" />
        <span title={accountName}>{accountName}</span>
        <button className="operator-icon" type="button" title={t("Sign out")} aria-label={t("Sign out")}
          disabled={signingOut} onClick={() => void logout()}><SignOut size={18} aria-hidden="true" /></button>
      </div>
      <div className="operator-preferences">
        <Translate size={18} aria-hidden="true" />
        <PayoutFilterSelect id="flowxpay-language" label={t("Language")}
          options={Object.entries(languages).map(([value, label]) => ({ value, label }))}
          value={locale} onChange={value => setLocale(resolveLocale(value))} />
        <button className="operator-icon" type="button" role="switch" aria-checked={darkTheme}
          aria-label={themeLabel} title={themeLabel} onClick={toggleTheme}>
          {darkTheme ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}
        </button>
      </div>
    </div>
  </>;
  return <div className={"app-shell member-shell console-shell " + workspace + "-shell"} data-sidebar-collapsed={narrow || sidebarCollapsed}
    data-payout-theme={darkTheme ? "dark" : "light"} data-order-page={orderPage}>
    <div className="platform-brand">
      {logo}
      <button type="button" className="sidebar-toggle" title={sidebarLabel} aria-label={sidebarLabel}
        aria-controls={expanded || !narrow ? navID : undefined} aria-haspopup={narrow ? "dialog" : undefined}
        aria-expanded={expanded} onClick={toggleSidebar}>
        <SidebarSimple size={20} aria-hidden="true" />
      </button>
    </div>
    <header className="topbar">
      {header}
      <span className="tag blue">{environment}</span>
    </header>
    {narrow ? mobileMenu && <Dialog className="operator-menu" title={title} close={() => setMobileMenu(false)}>
      {sidebar}
    </Dialog> : <aside className="operator-sidebar" hidden={sidebarCollapsed}>{sidebar}</aside>}
    {children}
  </div>;
}
