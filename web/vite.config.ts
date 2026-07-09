import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 開発時は Vite dev server(:5173) から Go API(:8080) にプロキシする。
// 本番は `npm run build` の成果物 (dist/) を API サーバーが配信する。
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
