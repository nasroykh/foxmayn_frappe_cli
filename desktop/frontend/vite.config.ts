import path from "path";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => ({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), tailwindcss(), wails("./bindings")],
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
