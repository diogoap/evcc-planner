<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { Rate } from '../api/types'
import { alpha, Chart, cssVar, hourTicks, onThemeChange, timeTick } from '../lib/chart'
import { clock, price } from '../lib/format'

const props = defineProps<{ slots: Rate[]; planned?: Rate[]; plannedLabel?: string; height?: number }>()

const canvas = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

// Stepped series: one point at each slot start plus the final slot end.
function steps(rates: Rate[]) {
  const pts = rates.map((r) => ({ x: Date.parse(r.start), y: r.value }))
  const last = rates[rates.length - 1]
  if (last) pts.push({ x: Date.parse(last.end), y: last.value })
  return pts
}

function plannedSteps() {
  const planned = props.planned ?? []
  if (!planned.length) return []
  const pts: { x: number; y: number | null }[] = []
  for (const r of planned) {
    pts.push({ x: Date.parse(r.start), y: r.value }, { x: Date.parse(r.end), y: r.value })
    pts.push({ x: Date.parse(r.end), y: null })
  }
  return pts
}

function render() {
  if (!canvas.value) return
  chart?.destroy()
  const grid = cssVar('--chart-grid')
  const text = cssVar('--chart-text')
  const charging = cssVar('--chart-charging')
  const line = cssVar('--chart-price')
  const first = props.slots[0]
  const last = props.slots[props.slots.length - 1]
  chart = new Chart(canvas.value, {
    type: 'line',
    data: {
      datasets: [
        {
          label: 'Published price incl. VAT',
          data: steps(props.slots),
          stepped: 'before',
          borderColor: line,
          borderWidth: 1.5,
          pointRadius: 0,
        },
        {
          label: props.plannedLabel ?? 'Planned charging',
          data: plannedSteps(),
          stepped: false,
          borderColor: charging,
          backgroundColor: alpha(charging, 0.33),
          fill: 'origin',
          pointRadius: 0,
          borderWidth: 0,
          spanGaps: false,
          hidden: !props.planned?.length,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      interaction: { mode: 'index', intersect: false },
      scales: {
        x: {
          type: 'linear',
          min: first ? Date.parse(first.start) : undefined,
          max: last ? Date.parse(last.end) : undefined,
          ticks: { callback: timeTick, color: text, maxRotation: 0, autoSkip: true },
          afterBuildTicks: hourTicks(3),
          grid: { color: grid },
        },
        y: {
          beginAtZero: true,
          ticks: { callback: (v) => `€${Number(v).toFixed(2)}`, color: text },
          grid: { color: grid },
        },
      },
      plugins: {
        legend: { labels: { color: text, boxWidth: 12, filter: (i) => !(i.hidden && i.datasetIndex === 1) } },
        tooltip: {
          filter: (i) => i.datasetIndex === 0,
          callbacks: {
            title: (items) => (items[0] ? clock(items[0].parsed.x as number) : ''),
            label: (item) => price(item.parsed.y as number) + ' incl. VAT',
          },
        },
      },
    },
  })
}

let stopTheme = () => {}
onMounted(() => {
  render()
  stopTheme = onThemeChange(render)
})
watch(() => [props.slots, props.planned], render)
onUnmounted(() => {
  stopTheme()
  chart?.destroy()
})
</script>

<template>
  <div class="chart-box" :style="height ? { height: `${height}px` } : undefined">
    <canvas ref="canvas" role="img" aria-label="Published electricity prices including VAT" />
  </div>
</template>
