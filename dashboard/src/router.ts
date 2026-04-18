import { createRouter, createWebHistory } from "vue-router";

import TeamView from "./views/TeamView.vue";
import AutomationView from "./views/AutomationView.vue";
import ConversationsView from "./views/ConversationsView.vue";
import TraceView from "./views/TraceView.vue";
import OTelTracesView from "./views/OTelTracesView.vue";
import MetricsView from "./views/MetricsView.vue";

// Route table backs the nav in App.vue one-to-one (#470). Add a view here
// and a nav entry there; dashboard picks up the new link automatically.
//
// Jobs / Tasks / Triggers / Webhooks / Continuations / Heartbeat all
// collapsed into `/automation` as card sections (#automation-v1). The
// legacy paths redirect there so existing bookmarks still work.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", name: "team", component: TeamView },
    { path: "/automation", name: "automation", component: AutomationView },
    // Legacy redirects — keep working for one release at minimum so any
    // bookmarks or linked docs don't 404 silently.
    { path: "/jobs", redirect: { name: "automation" } },
    { path: "/tasks", redirect: { name: "automation" } },
    { path: "/triggers", redirect: { name: "automation" } },
    { path: "/webhooks", redirect: { name: "automation" } },
    { path: "/continuations", redirect: { name: "automation" } },
    { path: "/heartbeat", redirect: { name: "automation" } },
    { path: "/conversations", name: "conversations", component: ConversationsView },
    { path: "/trace", name: "trace", component: TraceView },
    // Tool audit rows now land in tool-activity.jsonl with event_type='tool_audit'
    // and render through the Tool Trace tab. Legacy path redirects so
    // deep links and bookmarks keep working for at least one release.
    { path: "/tool-audit", redirect: { name: "trace" } },
    // OTel distributed-trace viewer (#632). The detail drawer is driven by
    // the /:traceId param so conversation rows can deep-link to a specific
    // trace (see ConversationsView.vue "Open trace" action).
    { path: "/otel-traces", name: "otel-traces", component: OTelTracesView },
    {
      path: "/otel-traces/:traceId",
      name: "otel-traces-detail",
      component: OTelTracesView,
    },
    { path: "/metrics", name: "metrics", component: MetricsView },
  ],
});
