<script setup lang="ts">
import { ref, onMounted } from "vue";

// Placeholder "team" view — first view to reach parity with the legacy ui/.
// Hits the harness `/team` endpoint through the /api proxy (vite dev or nginx
// prod). Once this is stable we grow into conversations, triggers, jobs, etc.
// See #470 and dashboard/README.md for the parity plan.

interface TeamMember {
  name: string;
  url: string;
  description?: string;
}

const members = ref<TeamMember[]>([]);
const error = ref<string>("");
const loading = ref<boolean>(true);

onMounted(async () => {
  try {
    const resp = await fetch("/api/team");
    if (!resp.ok) {
      error.value = `HTTP ${resp.status}`;
      return;
    }
    const data = (await resp.json()) as TeamMember[] | { team: TeamMember[] };
    members.value = Array.isArray(data) ? data : data.team || [];
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <section class="team-view">
    <h2 class="heading">Team</h2>
    <p class="description">
      Members of this agent deployment, sourced from the harness
      <code>/team</code> endpoint.
    </p>

    <div v-if="loading" class="state" data-testid="team-loading">Loading…</div>
    <div v-else-if="error" class="state state-error" data-testid="team-error">
      Could not load team: {{ error }}
    </div>
    <ul v-else-if="members.length" class="team-list" data-testid="team-list">
      <li v-for="m in members" :key="m.name" class="team-item">
        <div class="team-name">{{ m.name }}</div>
        <div class="team-url">{{ m.url }}</div>
        <div v-if="m.description" class="team-desc">{{ m.description }}</div>
      </li>
    </ul>
    <div v-else class="state" data-testid="team-empty">No team members configured.</div>
  </section>
</template>

<style scoped>
.heading {
  margin: 0 0 0.5rem;
  font-size: 1.4rem;
}

.description {
  color: var(--nyx-muted);
  margin: 0 0 1.5rem;
}

.state {
  padding: 1rem;
  color: var(--nyx-muted);
}

.state-error {
  color: #ff9090;
}

.team-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 0.75rem;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
}

.team-item {
  background: #181c26;
  border: 1px solid #222636;
  border-radius: 6px;
  padding: 1rem;
}

.team-name {
  font-weight: 600;
  margin-bottom: 0.25rem;
}

.team-url {
  color: var(--nyx-muted);
  font-family: ui-monospace, "SFMono-Regular", monospace;
  font-size: 0.85rem;
  word-break: break-all;
}

.team-desc {
  color: var(--nyx-fg);
  margin-top: 0.5rem;
  font-size: 0.9rem;
}
</style>
