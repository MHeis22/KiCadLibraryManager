import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [wails("./bindings")],
  server: {
    // Wails' dev asset proxy dials IPv4 127.0.0.1. Without this, Vite binds to
    // "localhost" which resolves to IPv6 ::1 on Windows/Node, so the proxy gets
    // "connection refused". Binding to the IPv4 address Wails dials fixes it.
    host: "127.0.0.1",
  },
});
