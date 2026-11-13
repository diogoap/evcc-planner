<script setup lang="ts">
// Small line icons drawn on a 24×24 grid. Decorative unless given a label.
export type IconName =
  | 'arrow-down'
  | 'battery'
  | 'bolt'
  | 'calendar'
  | 'check'
  | 'chevron'
  | 'chevron-left'
  | 'chevron-right'
  | 'clock'
  | 'close'
  | 'columns'
  | 'dash'
  | 'home'
  | 'info'
  | 'list'
  | 'plug'
  | 'plus'
  | 'refresh'
  | 'repeat'
  | 'route'
  | 'rows'
  | 'sliders'
  | 'solar-panel'
  | 'sun'
  | 'tag'
  | 'thermometer'
  | 'warning'
  | 'wind'

defineProps<{ name: IconName; label?: string }>()

const paths: Record<IconName, string[]> = {
  'arrow-down': ['M12 5v14', 'M6 13l6 6 6-6'],
  battery: ['M4 7h13a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2z', 'M22 11v2'],
  bolt: ['M13 2 4 14h7l-1 8 9-12h-7l1-8z'],
  calendar: [
    'M5 4h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2z',
    'M16 2v4',
    'M8 2v4',
    'M3 10h18',
  ],
  check: ['M5 12.5l4.5 4.5L19 7'],
  chevron: ['M6 9l6 6 6-6'],
  'chevron-left': ['M15 18l-6-6 6-6'],
  'chevron-right': ['M9 18l6-6-6-6'],
  clock: ['M12 3a9 9 0 1 0 0 18 9 9 0 1 0 0-18z', 'M12 7v5l3 2'],
  close: ['M18 6 6 18', 'M6 6l12 12'],
  columns: ['M4 4h4v16H4z', 'M10 4h4v16h-4z', 'M16 4h4v16h-4z'],
  dash: ['M6 12h12'],
  home: ['M3 10.5 12 3l9 7.5', 'M5 9v11h14V9', 'M10 20v-6h4v6'],
  info: ['M12 3a9 9 0 1 0 0 18 9 9 0 1 0 0-18z', 'M12 16v-5', 'M12 8h.01'],
  list: ['M8 6h13', 'M8 12h13', 'M8 18h13', 'M3 6h.01', 'M3 12h.01', 'M3 18h.01'],
  plug: ['M9 2v6', 'M15 2v6', 'M6 8h12v3a6 6 0 0 1-12 0z', 'M12 17v5'],
  plus: ['M12 5v14', 'M5 12h14'],
  refresh: ['M21 12a9 9 0 1 1-2.6-6.4', 'M21 3v6h-6'],
  repeat: ['M17 2l4 4-4 4', 'M3 11V9a3 3 0 0 1 3-3h15', 'M7 22l-4-4 4-4', 'M21 13v2a3 3 0 0 1-3 3H3'],
  route: [
    'M6 17a2 2 0 1 0 0 4 2 2 0 1 0 0-4z',
    'M18 3a2 2 0 1 0 0 4 2 2 0 1 0 0-4z',
    'M8 19h8.5a3.5 3.5 0 0 0 0-7h-9a3.5 3.5 0 0 1 0-7H16',
  ],
  rows: ['M4 4h16v4H4z', 'M4 10h16v4H4z', 'M4 16h16v4H4z'],
  'solar-panel': ['M3 4h18v10H3z', 'M3 9h18', 'M9 4v10', 'M15 4v10', 'M12 14v4', 'M8 20h8'],
  sliders: [
    'M4 21v-7',
    'M4 10V3',
    'M12 21v-9',
    'M12 8V3',
    'M20 21v-5',
    'M20 12V3',
    'M1 14h6',
    'M9 8h6',
    'M17 16h6',
  ],
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
  tag: ['M20.6 13.4 13.4 20.6a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8z', 'M7.5 7.5h.01'],
  thermometer: ['M14 14.8V4a2 2 0 1 0-4 0v10.8a4 4 0 1 0 4 0z'],
  warning: [
    'M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
    'M12 9v4',
    'M12 17h.01',
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
