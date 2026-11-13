<script setup lang="ts">
import type { Chart as ChartJS, Plugin, ScriptableContext } from 'chart.js'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Block, Rate } from '../api/types'
import { alpha, Chart, cssVar, hourTicks, onThemeChange, timeTick } from '../lib/chart'
import { clock, dayShort, localDate, price } from '../lib/format'
import { awayAt, awaySpans, priceStats } from '../lib/planchart'

// The published prices in detail, like EVCC's grid price chart: the price
// axis spans just the prices shown, so valleys and peaks stand out, with the
// cheapest and dearest slot labelled. A readout above follows the pointer or
// finger (now, when given); planned charging and time away (given the
// departures) are shaded.
const props = defineProps<{
  slots: Rate[]
  planned?: Rate[]
  plannedLabel?: string
  height?: number
  now?: string
  blocks?: Block[]
}>()

const canvas = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

const ms = (v: string) => Date.parse(v)
const stats = computed(() => priceStats(props.slots))
const cursor = ref<number | null>(null)
const nowMs = computed(() => (props.now ? ms(props.now) : null))

const slotAt = (t: number) => props.slots.find((r) => ms(r.start) <= t && t < ms(r.end))
const away = computed(() => {
  const first = props.slots[0]
  const last = props.slots[props.slots.length - 1]
  return props.blocks && first && last ? awaySpans(props.blocks, ms(first.start), ms(last.end)) : []
})
const line = computed(() => {
  const t = cursor.value ?? nowMs.value
  const slot = t == null ? undefined : slotAt(t)
  if (t == null || !slot) return ''
  const when = cursor.value == null ? 'Now' : `${dayShort(localDate(t))}, ${clock(t)}`
  const planned = props.planned?.some((r) => ms(r.start) <= t && t < ms(r.end))
  const gone = props.blocks ? awayAt(props.blocks, t) : null
  return [
    when,
    price(slot.value),
    planned && (props.plannedLabel ?? 'planned charging').toLowerCase(),
    gone && `away: ${gone.titles.join(', ') || gone.firstTitle}`,
  ]
    .filter(Boolean)
    .join(' · ')
})
const when = (v: string) => `${dayShort(localDate(v))} ${clock(v)}`
const summary = computed(() => {
  const s = stats.value
  const last = props.slots[props.slots.length - 1]
  if (!s || !last) return ''
  return `Average ${price(s.average)} · lowest ${price(s.min.value)} (${when(s.min.start)}) · highest ${price(s.max.value)} (${when(s.max.start)}) · until ${when(last.end)}`
})

// One point at each slot start plus the final slot end, drawn as steps.
function steps(rates: Rate[]) {
  const pts = rates.map((r) => ({ x: ms(r.start), y: r.value as number | null }))
  const last = rates[rates.length - 1]
  if (last) pts.push({ x: ms(last.end), y: last.value })
  return pts
}

function plannedSteps() {
  return (props.planned ?? []).flatMap((r) => [
    { x: ms(r.start), y: r.value as number | null },
    { x: ms(r.end), y: r.value },
    { x: ms(r.end), y: null },
  ])
}

// A soft fill under the price line, fading to the bottom.
function fade(color: string) {
  return (ctx: ScriptableContext<'line'>) => {
    const area = ctx.chart.chartArea
    if (!area) return alpha(color, 0.12)
    const g = ctx.chart.ctx.createLinearGradient(0, area.top, 0, area.bottom)
    g.addColorStop(0, alpha(color, 0.28))
    g.addColorStop(1, alpha(color, 0.02))
    return g
  }
}

// A rounded label with the price, above (dearest) or below (cheapest) its slot.
function label(c: ChartJS, r: Rate, color: string, above: boolean) {
  const { ctx, chartArea: area, scales } = c
  const text = `€${r.value.toFixed(2)}`
  ctx.font = `700 11px ${cssVar('--font')}`
  const w = ctx.measureText(text).width + 10
  const h = 18
  const x = Math.min(
    Math.max(scales.x!.getPixelForValue((ms(r.start) + ms(r.end)) / 2) - w / 2, area.left),
    area.right - w,
  )
  const py = scales.y!.getPixelForValue(r.value)
  const y = above ? Math.max(py - h - 4, area.top) : Math.min(py + 4, area.bottom - h)
  ctx.fillStyle = color
  ctx.beginPath()
  ctx.roundRect(x, y, w, h, 5)
  ctx.fill()
  ctx.fillStyle = cssVar('--chart-label-text')
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText(text, x + w / 2, y + h / 2 + 0.5)
}

const marks: Plugin<'line'> = {
  id: 'price-marks',
  // Time away from home, named where the name fits.
  beforeDatasetsDraw(c) {
    const { ctx, chartArea: area, scales } = c
    ctx.save()
    ctx.font = `600 11px ${cssVar('--font')}`
    ctx.textBaseline = 'top'
    for (const s of away.value) {
      const [x0, x1] = [scales.x!.getPixelForValue(s.start), scales.x!.getPixelForValue(s.end)]
      ctx.fillStyle = alpha(cssVar('--chart-away'), 0.14)
      ctx.fillRect(x0, area.top, x1 - x0, area.bottom - area.top)
      if (ctx.measureText(s.label).width + 6 < x1 - x0) {
        ctx.fillStyle = cssVar('--chart-text')
        ctx.fillText(s.label, x0 + 3, area.top + 3)
      }
    }
    ctx.restore()
  },
  afterDatasetsDraw(c) {
    const { ctx, chartArea: area, scales } = c
    const mark = (t: number, color: string, dash: number[]) => {
      const px = scales.x!.getPixelForValue(t)
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
    if (nowMs.value != null) mark(nowMs.value, cssVar('--chart-now'), [3, 3])
    if (cursor.value != null) mark(cursor.value, alpha(cssVar('--chart-now'), 0.6), [])
    const s = stats.value
    if (s && s.max.value > s.min.value) {
      label(c, s.max, cssVar('--chart-high'), true)
      label(c, s.min, cssVar('--chart-low'), false)
    }
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
    cursor.value = c.scales.x!.getValueForPixel(e.x) ?? null
  },
}

function render() {
  if (!canvas.value) return
  chart?.destroy()
  const grid = cssVar('--chart-grid')
  const text = cssVar('--chart-text')
  const color = cssVar('--chart-price')
  const charging = cssVar('--chart-charging')
  const first = props.slots[0]
  const last = props.slots[props.slots.length - 1]
  // Just the range of the prices, with room for the labels.
  const s = stats.value
  const pad = s ? Math.max((s.max.value - s.min.value) * 0.25, 0.01) : 0
  chart = new Chart(canvas.value, {
    type: 'line',
    data: {
      datasets: [
        {
          label: 'Published price incl. VAT',
          data: steps(props.slots),
          stepped: 'before',
          borderColor: color,
          backgroundColor: fade(color),
          fill: 'start',
          borderWidth: 1.5,
          pointRadius: 0,
        },
        {
          label: props.plannedLabel ?? 'Planned charging',
          data: plannedSteps(),
          borderColor: charging,
          backgroundColor: alpha(charging, 0.45),
          fill: 'start',
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
      events: ['mousemove', 'mouseout', 'click', 'touchstart', 'touchmove'],
      plugins: { legend: { display: false }, tooltip: { enabled: false } },
      scales: {
        x: {
          type: 'linear',
          min: first ? ms(first.start) : undefined,
          max: last ? ms(last.end) : undefined,
          ticks: { callback: timeTick, color: text, maxRotation: 0, autoSkip: true },
          afterBuildTicks: hourTicks(3),
          grid: { color: grid },
          border: { display: false },
        },
        y: {
          min: s ? Math.max(0, Math.floor((s.min.value - pad) * 100) / 100) : undefined,
          max: s ? Math.ceil((s.max.value + pad) * 100) / 100 : undefined,
          // Round labels only, not the zoomed edges.
          ticks: {
            maxTicksLimit: 5,
            includeBounds: false,
            callback: (v) => `€${Number(v).toFixed(2)}`,
            color: text,
          },
          grid: { color: grid },
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
watch(() => [props.slots, props.planned, props.blocks], render)
watch(cursor, () => chart?.draw())
onUnmounted(() => {
  stopTheme()
  chart?.destroy()
})
</script>

<template>
  <div class="price-chart">
    <p v-if="line" class="chart-readout small num" data-testid="price-readout">{{ line }}</p>
    <div class="chart-box" :style="height ? { height: `${height}px` } : undefined">
      <canvas
        ref="canvas"
        role="img"
        :aria-label="`Published electricity prices including VAT. ${summary}`"
      />
    </div>
    <p v-if="summary" class="small muted m-0" data-testid="price-summary">{{ summary }}</p>
  </div>
</template>

<style scoped>
.price-chart {
  display: grid;
  gap: var(--space-2);
}

.chart-readout {
  margin: 0;
  min-height: 1.45em;
}

/* Horizontal drags move the readout; vertical ones still scroll the page. */
canvas {
  touch-action: pan-y;
}
</style>
