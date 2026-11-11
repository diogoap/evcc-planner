<script setup lang="ts">
// Small line icons drawn on a 24×24 grid. Decorative unless given a label.
export type IconName = 'bolt' | 'chevron' | 'clock' | 'check' | 'dash' | 'sun' | 'wind'

defineProps<{ name: IconName; label?: string }>()

const paths: Record<IconName, string[]> = {
  bolt: ['M13 2 4 14h7l-1 8 9-12h-7l1-8z'],
  chevron: ['M6 9l6 6 6-6'],
  clock: ['M12 3a9 9 0 1 0 0 18 9 9 0 1 0 0-18z', 'M12 7v5l3 2'],
  check: ['M5 12.5l4.5 4.5L19 7'],
  dash: ['M6 12h12'],
  sun: [
    'M12 8a4 4 0 1 0 0 8 4 4 0 1 0 0-8z',
    'M12 2v2',
    'M12 20v2',
    'M2 12h2',
    'M20 12h2',
    'M4.9 4.9l1.4 1.4',
    'M17.7 17.7l1.4 1.4',
    'M4.9 19.1l1.4-1.4',
    'M17.7 6.3l1.4-1.4',
  ],
  wind: ['M3 8h10a3 3 0 1 0-3-3', 'M3 16h13a3 3 0 1 1-3 3', 'M3 12h7'],
}
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
