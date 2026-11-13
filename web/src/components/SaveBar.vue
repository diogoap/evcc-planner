<script setup lang="ts">
// A form's save button (and any buttons beside it, in the slot). While there
// are unsaved changes it says so, offers Discard, and stays in view at the
// bottom of the screen; the other buttons wait until the form is saved.
defineProps<{ dirty: boolean; saving?: boolean; label: string; testid?: string }>()
const emit = defineEmits<{ discard: [] }>()
</script>

<template>
  <div class="save-bar" :class="{ dirty }" data-testid="save-bar">
    <template v-if="dirty">
      <span class="save-status small strong" role="status">Unsaved changes</span>
      <span class="spacer" />
      <button class="btn" type="button" @click="emit('discard')">Discard</button>
      <button class="btn primary" type="submit" :disabled="saving" :data-testid="testid">{{ label }}</button>
    </template>
    <template v-else>
      <button class="btn primary" type="submit" :disabled="saving" :data-testid="testid">{{ label }}</button>
      <slot />
    </template>
  </div>
</template>

<style scoped>
.save-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.save-bar.dirty {
  position: sticky;
  bottom: calc(var(--space-3) + env(safe-area-inset-bottom));
  z-index: 5;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
  box-shadow: var(--shadow-lg);
}

/* Phones: the status on its own line, the buttons below it on the right. */
@media (max-width: 640px) {
  .save-bar.dirty {
    bottom: calc(var(--bottom-nav-h) + var(--space-2) + env(safe-area-inset-bottom));
  }

  .save-status {
    flex-basis: 100%;
  }
}
</style>
