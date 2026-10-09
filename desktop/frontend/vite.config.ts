/// <reference types="vitest/config" />
import path from "path";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// `vite dev` needs an inline script (the React refresh preamble), inline styles
// (HMR style tags) and a websocket, so it serves a looser CSP than index.html
// carries. Production builds keep the strict one untouched.
function devCSP(): Plugin {
  return {
    name: "ffd-dev-csp",
    apply: "serve",
    transformIndexHtml(html) {
      // Start from the policy in index.html so the two cannot drift.
      return html.replace(/(http-equiv="Content-Security-Policy"\s+content=")([^"]*)"/, (_m, head, policy: string) => {
        const extra: Record<string, string> = {
          "script-src": "'unsafe-inline'",
          "style-src": "'unsafe-inline'",
          "connect-src": "ws: wss:",
        };
        const out = policy
          .split(";")
          .map((d) => d.trim())
          .filter(Boolean)
          .map((d) => {
            const name = d.split(/\s+/)[0];
            return extra[name] ? `${d} ${extra[name]}` : d;
          });
        return `${head}${out.join("; ")}"`;
      });
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => ({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), tailwindcss(), wails("./bindings"), devCSP()],
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
  },
  resolve: {
    alias: [
      // `vite --mode mock` (npm run dev:mock) swaps the Wails bindings for a
      // fake backend so the UI runs in a plain browser. Only that mode sees
      // src/mock; production builds never import it.
      ...(mode === "mock"
        ? [{ find: /^@\/lib\/backend$/, replacement: path.resolve(import.meta.dirname, "./src/mock/backend.ts") }]
        : []),
      { find: "@", replacement: path.resolve(import.meta.dirname, "./src") },
    ],
  },
}));
