import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { LocaleProvider } from "./locale";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("FlowXPay mount point is missing");

const appRoot = createRoot(root);
function mount(Component: typeof App) {
  appRoot.render(
    <React.StrictMode>
      <LocaleProvider><Component /></LocaleProvider>
    </React.StrictMode>,
  );
}

if (import.meta.env.MODE === "community-demo") {
  const url = new URL(window.location.href);
  const lang = url.searchParams.get("lang");
  if (lang === "zh" || lang === "en") {
    url.searchParams.set("lang", lang === "zh" ? "zh-CN" : "en-US");
    window.history.replaceState(null, "", url);
  }
  void import("./demo/DemoApp").then(({ App: DemoApp }) => mount(DemoApp));
} else {
  mount(App);
}
