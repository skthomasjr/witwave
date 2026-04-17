import { createRouter, createWebHistory } from "vue-router";

import TeamView from "./views/TeamView.vue";
import JobsView from "./views/JobsView.vue";
import TasksView from "./views/TasksView.vue";
import TriggersView from "./views/TriggersView.vue";
import WebhooksView from "./views/WebhooksView.vue";
import ContinuationsView from "./views/ContinuationsView.vue";
import HeartbeatView from "./views/HeartbeatView.vue";
import ConversationsView from "./views/ConversationsView.vue";
import TraceView from "./views/TraceView.vue";
import ToolAuditView from "./views/ToolAuditView.vue";
import OTelTracesView from "./views/OTelTracesView.vue";
import MetricsView from "./views/MetricsView.vue";
import CalendarView from "./views/CalendarView.vue";

// Route table backs the nav in App.vue one-to-one (#470). Add a view here
// and a nav entry there; dashboard picks up the new link automatically.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", name: "team", component: TeamView },
    { path: "/calendar", name: "calendar", component: CalendarView },
    { path: "/jobs", name: "jobs", component: JobsView },
    { path: "/tasks", name: "tasks", component: TasksView },
    { path: "/triggers", name: "triggers", component: TriggersView },
    { path: "/webhooks", name: "webhooks", component: WebhooksView },
    { path: "/continuations", name: "continuations", component: ContinuationsView },
    { path: "/heartbeat", name: "heartbeat", component: HeartbeatView },
    { path: "/conversations", name: "conversations", component: ConversationsView },
    { path: "/trace", name: "trace", component: TraceView },
    { path: "/tool-audit", name: "tool-audit", component: ToolAuditView },
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
