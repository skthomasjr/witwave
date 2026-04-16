import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// Keep this config minimal — the dashboard is the *future* UI (#470) and we want
// scaffolding churn to be small as the tree grows. /api/* is proxied to the
// nyx-harness so a `npm run dev` session can be pointed at a running harness
// without CORS gymnastics. In production the nginx layer fronts the static
// build and performs the same proxy (see Dockerfile / nginx.conf).
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.VITE_HARNESS_URL || "http://localhost:8000",
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
  },
  test: {
    environment: "jsdom",
    globals: true,
  },
});
