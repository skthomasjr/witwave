import { defineConfig, type ProxyOptions } from "vite";
import vue from "@vitejs/plugin-vue";

// `npm run dev` mirrors the production nginx routing (#470):
//   /api/team                    → served locally from VITE_TEAM (no agent call)
//   /api/agents/<name>/...       → proxied to that agent's own harness
//   /api/...                     → 404 (matches prod nginx behaviour)
//
// VITE_TEAM is a JSON string like:
//   [{"name":"bob","url":"http://localhost:8099"},{"name":"fred","url":"http://localhost:8098"}]
// Each URL should point at a kubectl port-forward of that agent's witwave service.
// When VITE_TEAM is unset only static routes serve; /api/* won't work.

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
      rewrite: (path) => path.replace(new RegExp(`^/api/agents/${entry.name}/`), "/"),
    };
  }

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
    // tests/setup/axe.ts wires vitest-axe's toHaveNoViolations matcher
    // into `expect`. Individual specs can still run without a11y
    // coverage — the setup file only registers the matcher, it doesn't
    // force checks (#970).
    setupFiles: ["./tests/setup/axe.ts"],
    // Playwright specs live under tests/e2e (#818) and use `test` from
    // @playwright/test; keep vitest from picking them up.
    exclude: ["**/node_modules/**", "**/dist/**", "tests/e2e/**"],
  },
});
