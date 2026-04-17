import { defineConfig, type ProxyOptions } from "vite";
import vue from "@vitejs/plugin-vue";

// `npm run dev` needs to mirror the production nginx routing (#470):
//   /api/team                    → served locally from VITE_TEAM (no agent call)
//   /api/agents/<name>/...       → proxied to that agent's own harness
//   /api/...                     → legacy fallback, proxied to VITE_HARNESS_URL
//
// VITE_TEAM is a JSON string like:
//   [{"name":"bob","url":"http://localhost:8099"},{"name":"fred","url":"http://localhost:8098"}]
// Each URL should point at a kubectl port-forward of that agent's nyx service.
// When VITE_TEAM is unset the dev server degrades to the legacy single-harness
// mode so existing setups keep working without extra env configuration.

interface TeamEntry {
  name: string;
  url: string;
}

function parseTeam(): TeamEntry[] {
  const raw = process.env.VITE_TEAM;
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (e): e is TeamEntry =>
        typeof e === "object" &&
        e !== null &&
        typeof (e as TeamEntry).name === "string" &&
        typeof (e as TeamEntry).url === "string",
    );
  } catch (e) {
    // eslint-disable-next-line no-console
    console.warn("[vite] VITE_TEAM is not valid JSON, falling back to legacy mode:", e);
    return [];
  }
}

function buildProxy(): Record<string, ProxyOptions> {
  const proxy: Record<string, ProxyOptions> = {};
  const team = parseTeam();

  // Serve /api/team locally when a team list is configured — matches the
  // production nginx behavior where the dashboard pod returns this inline.
  if (team.length > 0) {
    proxy["^/api/team$"] = {
      target: "http://localhost:0",
      changeOrigin: false,
      configure: (proxyServer) => {
        proxyServer.on("proxyReq", (_proxyReq, req, res) => {
          const body = JSON.stringify(team);
          res.writeHead(200, {
            "Content-Type": "application/json",
            "Content-Length": Buffer.byteLength(body),
          });
          res.end(body);
        });
      },
    };
  }

  // Per-agent direct routes.
  for (const entry of team) {
    const pattern = `^/api/agents/${entry.name}/`;
    proxy[pattern] = {
      target: entry.url,
      changeOrigin: true,
      rewrite: (path) =>
        path.replace(new RegExp(`^/api/agents/${entry.name}/`), "/"),
    };
  }

  // Legacy catch-all — matches the production nginx's /api/ fallback.
  proxy["/api"] = {
    target: process.env.VITE_HARNESS_URL || "http://localhost:8000",
    changeOrigin: true,
    rewrite: (path) => path.replace(/^\/api/, ""),
  };

  return proxy;
}

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: buildProxy(),
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
