import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Backend runs on :8080 by default, proxy /api to it so the SPA can use
// same-origin fetch/websocket calls without CORS headaches in dev.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
});
