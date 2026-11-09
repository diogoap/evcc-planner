<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { Block, TimelinePoint } from '../api/types'
import { Chart, cssVar, hourTicks, timeTick } from '../lib/chart'
import { dateTime, pct } from '../lib/format'

const props = defineProps<{
  timeline: TimelinePoint[]
  blocks: Block[]
  from: string
  to: string
  reserve: number
}>()

const canvas = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

function datasets() {
  const accent = cssVar('--accent')
  const info = cssVar('--info')
  const muted = cssVar('--muted')
  const future = props.blocks.filter((b) => b.status !== 'done' && b.status !== 'in_progress')
  return [
    {
      type: 'line' as const,
      label: 'Projected SoC',
      data: props.timeline.map((p) => ({ x: Date.parse(p.at), y: p.soc })),
      borderColor: accent,
      backgroundColor: accent + '22',
      fill: 'origin' as const,
      pointRadius: 0,
      borderWidth: 2,
      tension: 0,
    },
    {
      type: 'scatter' as const,
      label: 'Required at departure',
      data: future.map((b) => ({ x: Date.parse(b.departAt), y: b.requiredSoc })),
      borderColor: info,
      backgroundColor: info,
      pointStyle: 'triangle' as const,
      pointRadius: 6,
    },
    {
      type: 'line' as const,
      label: 'Arrival reserve',
      data: [
        { x: Date.parse(props.from), y: props.reserve },
        { x: Date.parse(props.to), y: props.reserve },
      ],
      borderColor: muted,
      borderDash: [4, 4],
      borderWidth: 1,
      pointRadius: 0,
    },
  ]
}

function render() {
  if (!canvas.value) return
  chart?.destroy()
  const grid = cssVar('--chart-grid')
  const text = cssVar('--muted')
  chart = new Chart(canvas.value, {
    type: 'line',
    data: { datasets: datasets() },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      interaction: { mode: 'nearest', intersect: false },
      scales: {
        x: {
          type: 'linear',
          min: Date.parse(props.from),
          max: Date.parse(props.to),
          ticks: { callback: timeTick, color: text, maxRotation: 0, autoSkip: true },
          afterBuildTicks: hourTicks(12),
          grid: { color: grid },
        },
        y: {
          min: 0,
          max: 100,
          ticks: { stepSize: 25, callback: (v) => `${v}%`, color: text },
          grid: { color: grid },
        },
      },
      plugins: {
        legend: { labels: { color: text, boxWidth: 12 } },
        tooltip: {
          callbacks: {
            title: (items) => (items[0] ? dateTime(items[0].parsed.x as number) : ''),
            label: (item) => `${item.dataset.label}: ${pct(item.parsed.y as number)}`,
          },
        },
      },
    },
  })
}

onMounted(render)
watch(() => [props.timeline, props.blocks], render)
onUnmounted(() => chart?.destroy())
</script>

<template>
  <div class="chart-box">
    <canvas
      ref="canvas"
      role="img"
      aria-label="Projected state of charge over the next seven days with required departure levels"
    />
  </div>
</template>
