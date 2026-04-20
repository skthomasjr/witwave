<script setup lang="ts">
import { useAlerts } from "../composables/useAlerts";

// Global alert banner (#1108). Always rendered at the top of the app
// main region; self-shows when useAlerts surfaces an active condition,
// self-hides when the alert clears or the user dismisses it.
//
// role=alert + aria-live=assertive so assistive tech announces it the
// moment it appears (the header status pill is polite; this surface
// is the loud one for incident triage).

const { active, dismiss } = useAlerts();
</script>

<template>
  <transition name="banner-fade">
    <div
      v-if="active"
      :key="active.id"
      class="alert-banner"
      :class="`sev-${active.severity}`"
      role="alert"
      aria-live="assertive"
      data-testid="global-alert-banner"
    >
      <i
        :class="
          active.severity === 'error'
            ? 'pi pi-times-circle'
            : active.severity === 'warning'
              ? 'pi pi-exclamation-triangle'
              : 'pi pi-info-circle'
        "
        class="sev-icon"
        aria-hidden="true"
      />
      <div class="content">
        <div class="title">{{ active.title }}</div>
        <div v-if="active.detail" class="detail">{{ active.detail }}</div>
      </div>
      <button
        type="button"
        class="dismiss"
        :title="`Dismiss ${active.title}`"
        aria-label="Dismiss alert"
        data-testid="global-alert-dismiss"
        @click="dismiss(active.id)"
      >
        <i class="pi pi-times" aria-hidden="true" />
      </button>
    </div>
  </transition>
</template>

<style scoped>
.alert-banner {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--witwave-border);
  font-family: var(--witwave-mono);
  font-size: 12px;
  line-height: 1.4;
}

.sev-error {
  background: rgba(255, 80, 80, 0.1);
  color: var(--witwave-red);
  border-bottom-color: var(--witwave-red);
}

.sev-warning {
  background: rgba(255, 200, 0, 0.08);
  color: var(--witwave-yellow);
  border-bottom-color: var(--witwave-yellow);
}

.sev-info {
  background: rgba(0, 180, 220, 0.08);
  color: var(--witwave-dim);
}

.sev-icon {
  margin-top: 2px;
}

.content {
  flex: 1;
}

.title {
  font-weight: 600;
  letter-spacing: 0.04em;
}

.detail {
  margin-top: 2px;
  opacity: 0.85;
}

.dismiss {
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  padding: 2px 6px;
  opacity: 0.7;
  transition: opacity 0.12s;
}

.dismiss:hover {
  opacity: 1;
}

.banner-fade-enter-active,
.banner-fade-leave-active {
  transition: opacity 0.18s;
}

.banner-fade-enter-from,
.banner-fade-leave-to {
  opacity: 0;
}
</style>
