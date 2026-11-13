<script setup lang="ts">
// Small line icons drawn on a 24×24 grid. Decorative unless given a label.
import { iconPaths as paths, type IconName } from '../lib/icons'

export type { IconName }

defineProps<{ name: IconName; label?: string }>()
</script>

<template>
  <svg
    class="icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
  >
    <path v-for="d in paths[name]" :key="d" :d="d" />
  </svg>
</template>
