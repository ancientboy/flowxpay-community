import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const target = process.env.FLOWXPAY_API_ORIGIN || "http://127.0.0.1:8091";

export default defineConfig(({ mode }) => ({
  cacheDir: ".vite",
  build: { outDir: "dist/client" },
  optimizeDeps: { include: ["react", "react-dom/client"] },
  server: {
    host: "127.0.0.1",
    proxy: { "/v1": { target }, "/sandbox": { target }, "/healthz": { target } },
    headers: { "Referrer-Policy": "no-referrer" },
    warmup: { clientFiles: ["./src/main.tsx"] },
  },
  plugins: [react(), mode === "community-demo" && {
    name: "community-demo-policy",
    transformIndexHtml: () => [{
      tag: "meta",
      attrs: {
        "http-equiv": "Content-Security-Policy",
        content: "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self'; base-uri 'none'; form-action 'none'",
      },
      injectTo: "head-prepend",
    }],
  }],
}));
