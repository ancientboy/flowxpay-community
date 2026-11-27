import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { LocaleProvider } from "./locale";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("FlowXPay mount point is missing");

createRoot(root).render(
  <React.StrictMode>
    <LocaleProvider><App /></LocaleProvider>
  </React.StrictMode>,
);
