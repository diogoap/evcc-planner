<script setup lang="ts">
import AppIcon from './AppIcon.vue'

// Something failed to load or save: what went wrong, with Retry.
defineProps<{ message: string }>()
const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="banner tone-bad row" role="alert" data-testid="error-banner">
    <AppIcon name="warning" />
    <span class="grow">{{ message }}</span>
    <button class="btn sm" type="button" @click="emit('retry')">Retry</button>
  </div>
</template>
