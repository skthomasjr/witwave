import { defineStore } from "pinia";

// Per-agent selection state was previously prop-drilled through
// TeamView → AgentList → AgentCard and TeamView → AgentDetail →
// ChatPanel, making cross-view features (e.g. opening a conversation
// from the Conversations view and surfacing it in Team) awkward (#748).
//
// This Pinia store is the canonical home for "which agent and which
// backend is currently selected?" Views bind to it; presentational
// components (AgentList / AgentDetail / ChatPanel) can still accept
// props so unit mounts don't have to install Pinia, but the view
// layer is no longer the source of truth.
//
// Kept deliberately small — only names/ids here. Agent/backend
// directory data lives in useTeam() (TanStack-style fetch composable)
// and should not be duplicated into the store.
export const useSelectionStore = defineStore("selection", {
  state: () => ({
    selectedName: null as string | null,
    activeBackendId: null as string | null,
  }),
  actions: {
    // Select an agent by name. Clears the backend selection because
    // the previous backend id won't exist under the new agent. If
    // cross-agent backend memory is ever wanted, add a per-agent
    // Map<name, backendId> cache here.
    selectAgent(name: string | null): void {
      this.selectedName = name;
      this.activeBackendId = null;
    },
    // Select both an agent and a backend at once — used when a
    // backend bubble on an agent card is clicked.
    selectBackend(name: string, backendId: string): void {
      this.selectedName = name;
      this.activeBackendId = backendId;
    },
    // Set only the backend (called when switching backends inside an
    // already-selected agent's detail pane).
    setActiveBackend(backendId: string | null): void {
      this.activeBackendId = backendId;
    },
    clear(): void {
      this.selectedName = null;
      this.activeBackendId = null;
    },
  },
});
