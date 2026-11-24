<script setup lang="ts">
import type { Plugin } from 'chart.js'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { DayStats } from '../api/types'
import { layout } from '../layout'
import { alpha, Chart, cssVar, onThemeChange } from '../lib/chart'
import {
  dailySeries,
  dateTicks,
  dayLine,
  dayTitle,
  pointHint,
  summarize,
  summaryText,
  tickLabel,
  type HistorySeries,
  type PointSeries,
} from '../lib/history'

// Each day's prices as a band from the lowest to the highest slot, with the
// average as a line; wind (Beaufort), sun (% of possible sunshine), driving
// (km, as bars), and the trips' energy and the solar forecast (sharing a kWh
// axis, to compare) on their own axes when shown
// (shorter labels on phones, where the readout gives each day's values). Days not recorded are gaps. A readout above
// follows the pointer or finger (today, otherwise); a dashed line marks
// today. Pointing at a dot shows a hint with its day, series, and value.
const props = defineProps<{ days: DayStats[]; today: string; show: Record<HistorySeries, boolean> }>()

const canvas = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

const series = computed(() => dailySeries(props.days))
const todayIndex = computed(() => series.value.findIndex((s) => s.date === props.today))
const cursor = ref<number | null>(null)

const line = computed(() => {
  const i = cursor.value ?? (todayIndex.value >= 0 ? todayIndex.value : series.value.length - 1)
  const s = series.value[i]
  return s ? dayLine(s.day, s.date, props.today) : ''
})
const summary = computed(() => {
  const s = summarize(props.days)
  return s ? summaryText(s) : 'No prices recorded yet.'
})

// The series of the datasets drawn with points, in the datasets' order;
// driving (bars) comes after them and gets no hint.
const pointSeries: PointSeries[] = ['high', 'low', 'average', 'wind', 'sun', 'energy', 'solar']

const point = (pick: (d: DayStats) => number | null) =>
  series.value.map((s, i) => ({ x: i, y: s.day ? pick(s.day) : null }))

const marks: Plugin<'line'> = {
  id: 'history-marks',
  afterDatasetsDraw(c) {
    const { ctx, chartArea: area, scales } = c
    const mark = (i: number, color: string, dash: number[]) => {
      const px = scales.x!.getPixelForValue(i)
      if (px < area.left || px > area.right) return
      ctx.strokeStyle = color
      ctx.lineWidth = 1
      ctx.setLineDash(dash)
      ctx.beginPath()
      ctx.moveTo(px, area.top)
      ctx.lineTo(px, area.bottom)
      ctx.stroke()
      ctx.setLineDash([])
    }
    ctx.save()
    if (todayIndex.value >= 0) mark(todayIndex.value, cssVar('--chart-now'), [3, 3])
    if (cursor.value != null) mark(cursor.value, alpha(cssVar('--chart-now'), 0.6), [])
    ctx.restore()
  },
  afterEvent(c, args) {
    const e = args.event
    if (e.type === 'mouseout') {
      cursor.value = null
      return
    }
    const area = c.chartArea
    if (e.x == null || e.x < area.left || e.x > area.right) return
    const i = Math.round(c.scales.x!.getValueForPixel(e.x) ?? -1)
    cursor.value = i >= 0 && i < series.value.length ? i : null
  },
}

function render() {
  if (!canvas.value) return
  chart?.destroy()
  const grid = cssVar('--chart-grid')
  const text = cssVar('--chart-text')
  const color = cssVar('--chart-price')
  const wind = cssVar('--chart-wind')
  const sun = cssVar('--chart-solar')
  const driving = cssVar('--chart-away')
  const energy = cssVar('--chart-required')
  const n = series.value.length
  const dates = series.value.map((s) => s.date)
  // Points while the days are few enough to tell apart.
  const dot = n <= 31 ? 2.5 : 0
  const show = props.show
  const compact = layout.compact
  // The price axis spans just the prices shown.
  const shown = props.days.flatMap((d) =>
    (show.range ? [d.priceMin, d.priceMax] : []).concat(show.average || !show.range ? [d.priceAvg] : []),
  )
  const values = shown.filter((v): v is number => v != null)
  const low = values.length ? Math.min(...values) : 0
  const high = values.length ? Math.max(...values) : 0.5
  const pad = Math.max((high - low) * 0.1, 0.01)
  // Each hint's colour box, at full strength (the band's lines are faint).
  const hintColor: Record<PointSeries, string> = {
    high: color,
    low: color,
    average: color,
    wind,
    sun,
    energy,
    solar: sun,
  }
  chart = new Chart(canvas.value, {
    type: 'line',
    data: {
      datasets: [
        {
          label: 'Highest price',
          data: point((d) => d.priceMax),
          borderColor: alpha(color, 0.45),
          backgroundColor: alpha(color, 0.18),
          pointBackgroundColor: alpha(color, 0.45),
          fill: '+1',
          borderWidth: 1,
          pointRadius: dot ? 1.5 : 0,
          hidden: !show.range,
        },
        {
          label: 'Lowest price',
          data: point((d) => d.priceMin),
          borderColor: alpha(color, 0.45),
          pointBackgroundColor: alpha(color, 0.45),
          borderWidth: 1,
          pointRadius: dot ? 1.5 : 0,
          hidden: !show.range,
        },
        {
          label: 'Average price',
          data: point((d) => d.priceAvg),
          borderColor: color,
          backgroundColor: color,
          borderWidth: 2,
          pointRadius: dot,
          hidden: !show.average,
        },
        {
          label: 'Wind',
          data: point((d) => d.windBft),
          yAxisID: 'wind',
          borderColor: wind,
          backgroundColor: wind,
          borderWidth: 1.5,
          pointRadius: dot ? 2 : 0,
          hidden: !show.wind,
        },
        {
          label: 'Sun',
          data: point((d) => d.sunshinePct),
          yAxisID: 'sun',
          borderColor: sun,
          backgroundColor: sun,
          borderWidth: 1.5,
          borderDash: [5, 3],
          pointRadius: dot ? 2 : 0,
          hidden: !show.sun,
        },
        {
          label: 'Energy',
          data: point((d) => d.energyKWh),
          yAxisID: 'kwh',
          borderColor: energy,
          backgroundColor: energy,
          borderWidth: 1.5,
          pointRadius: dot ? 2 : 0,
          hidden: !show.energy,
        },
        {
          // A soft area, unlike sun's dashed line in the same colour.
          label: 'Solar forecast',
          data: point((d) => d.solarKWh),
          yAxisID: 'kwh',
          borderColor: sun,
          backgroundColor: alpha(sun, 0.15),
          pointBackgroundColor: sun,
          fill: 'origin',
          borderWidth: 1.5,
          pointRadius: dot ? 2 : 0,
          hidden: !show.solar,
        },
        {
          // Behind the price lines (a higher order is drawn first).
          type: 'bar' as const,
          label: 'Driving',
          data: point((d) => d.distanceKm),
          yAxisID: 'km',
          order: 1,
          backgroundColor: alpha(driving, 0.3),
          borderColor: alpha(driving, 0.6),
          borderWidth: 1,
          barPercentage: 0.9,
          categoryPercentage: 0.8,
          hidden: !show.driving,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      parsing: false,
      events: ['mousemove', 'mouseout', 'click', 'touchstart', 'touchmove'],
      // Dots are easier to point at than they look; without dots, no hints.
      elements: { point: { hitRadius: dot ? 6 : 0 } },
      plugins: {
        legend: { display: false },
        // The hint inverts the page's colours, like a native tooltip.
        tooltip: {
          mode: 'point',
          intersect: true,
          filter: (item) => item.datasetIndex < pointSeries.length,
          backgroundColor: cssVar('--text'),
          titleColor: cssVar('--surface'),
          bodyColor: cssVar('--surface'),
          cornerRadius: 8,
          padding: 8,
          boxPadding: 4,
          callbacks: {
            title: (items) => {
              const s = series.value[items[0]?.parsed.x ?? -1]
              return s ? dayTitle(s.date, props.today) : ''
            },
            label: (item) =>
              item.parsed.y == null ? '' : pointHint(pointSeries[item.datasetIndex]!, item.parsed.y),
            labelColor: (item) => {
              const c = hintColor[pointSeries[item.datasetIndex]!]
              return { borderColor: c, backgroundColor: c }
            },
          },
        },
      },
      scales: {
        x: {
          type: 'linear',
          min: -0.5,
          max: n - 0.5,
          afterBuildTicks: (axis) => {
            axis.ticks = dateTicks(dates).map((value) => ({ value }))
          },
          ticks: {
            callback: (v) => tickLabel(dates[Number(v)] ?? '', n),
            color: text,
            maxRotation: 0,
            autoSkip: true,
          },
          grid: { color: grid },
          border: { display: false },
        },
        y: {
          min: Math.max(0, Math.floor((low - pad) * 100) / 100),
          max: Math.ceil((high + pad) * 100) / 100,
          ticks: {
            maxTicksLimit: 5,
            includeBounds: false,
            callback: (v) => `€${Number(v).toFixed(2)}`,
            color: text,
          },
          grid: { color: grid },
          border: { display: false },
        },
        wind: {
          type: 'linear',
          position: 'right',
          display: show.wind,
          min: 0,
          max: 12,
          ticks: { stepSize: compact ? 6 : 3, callback: (v) => (compact ? `${v}` : `${v} Bft`), color: wind },
          grid: { display: false },
          border: { display: false },
        },
        sun: {
          type: 'linear',
          position: 'right',
          display: show.sun,
          min: 0,
          max: 100,
          ticks: { stepSize: compact ? 50 : 25, callback: (v) => `${v}%`, color: text },
          grid: { display: false },
          border: { display: false },
        },
        km: {
          type: 'linear',
          position: 'right',
          display: show.driving,
          min: 0,
          suggestedMax: 50,
          ticks: { maxTicksLimit: 4, callback: (v) => (compact ? `${v}` : `${v} km`), color: text },
          grid: { display: false },
          border: { display: false },
        },
        kwh: {
          type: 'linear',
          position: 'right',
          display: show.energy || show.solar,
          min: 0,
          suggestedMax: 10,
          ticks: {
            maxTicksLimit: 4,
            callback: (v) => (compact ? `${v}` : `${v} kWh`),
            // In the series' colour while it is the only one on the axis.
            color: show.energy && show.solar ? text : show.solar ? sun : energy,
          },
          grid: { display: false },
          border: { display: false },
        },
      },
    },
    plugins: [marks],
  })
}

let stopTheme = () => {}
onMounted(() => {
  render()
  stopTheme = onThemeChange(render)
})
watch(() => [props.days, props.today, { ...props.show }, layout.compact], render)
watch(cursor, () => chart?.draw())
onUnmounted(() => {
  stopTheme()
  chart?.destroy()
})
</script>

<template>
  <div class="history-chart">
    <p class="chart-readout small num" data-testid="history-readout">{{ line }}</p>
    <div class="chart-box">
      <canvas
        ref="canvas"
        role="img"
        :aria-label="`Daily electricity prices including VAT: the lowest, average, and highest of each day. ${summary}`"
      />
    </div>
    <p class="small muted m-0" data-testid="history-summary">{{ summary }}</p>
  </div>
</template>

<style scoped>
.history-chart {
  display: grid;
  gap: var(--space-2);
}

.chart-readout {
  margin: 0;
  min-height: 1.45em;
  white-space: normal;
}

/* Horizontal drags move the readout; vertical ones still scroll the page. */
canvas {
  touch-action: pan-y;
}
</style>
