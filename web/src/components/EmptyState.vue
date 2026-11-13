<script setup lang="ts">
import type { IconName } from './AppIcon.vue'
import AppIcon from './AppIcon.vue'

// Nothing to show yet: what this place is for, and what to do next (slot).
defineProps<{ icon: IconName; title: string; text?: string }>()
</script>

<template>
  <div class="empty" data-testid="empty-state">
    <span class="empty-icon"><AppIcon :name="icon" /></span>
    <strong>{{ title }}</strong>
    <p v-if="text" class="small muted m-0">{{ text }}</p>
    <div v-if="$slots.default" class="row"><slot /></div>
  </div>
</template>

<style scoped>
.empty {
  display: grid;
  justify-items: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  text-align: center;
}

.empty-icon {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: var(--surface-2);
  color: var(--muted);
}

.empty-icon .icon {
  width: 22px;
  height: 22px;
}

.empty p {
  max-width: 36ch;
}
</style>
