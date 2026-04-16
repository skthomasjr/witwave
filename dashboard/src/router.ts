import { createRouter, createWebHistory } from "vue-router";

import TeamView from "./views/TeamView.vue";

// Keep the route table minimal while the dashboard grows to parity with the
// legacy ui/. Each parity feature becomes a named route here so the nav can
// reference it without guessing paths (#470).
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      name: "team",
      component: TeamView,
    },
  ],
});
