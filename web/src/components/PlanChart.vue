<script setup lang="ts">
import type { ChartDataset, Plugin } from 'chart.js'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import type { Block, DayAdvice, Rate, SolarPoint, TimelinePoint } from '../api/types'
import { alpha, Chart, cssVar, hourTicks, onThemeChange, timeTick } from '../lib/chart'
import {
  awaySpans,
  chartSummary,
  priceSpans,
  readout,
  type ChartData,
  type EstimateSpan,
} from '../lib/planchart'

// SoC above prices on one time axis, from the current hour to the end of the
// seven days. Two charts with equal axis widths, so their times line up; a
// readout above them follows the pointer or finger (now by default).
const props = defineProps<{
  timeline: TimelinePoint[]
  blocks: Block[]
  slots: Rate[]
  solar: SolarPoint[]
  /** The planner's estimate of the charging slots. */
  charging: Rate[]
  advice: DayAdvice[]
  dates: string[]
  reserve: number
  now: string
  to: string
}>()

const AXIS_LEFT = 48
const AXIS_RIGHT = 44

const socCanvas = ref<HTMLCanvasElement | null>(null)
const priceCanvas = ref<HTMLCanvasElement | null>(null)
let charts: Chart[] = []

const nowMs = computed(() => Date.parse(props.now))
const from = computed(() => Math.floor(nowMs.value / 3_600_000) * 3_600_000)
const to = computed(() => Date.parse(props.to))
const data = computed<ChartData>(() => ({
  timeline: props.timeline,
  blocks: props.blocks,
  slots: props.slots,
  advice: props.advice,
  solar: props.solar,
  charging: props.charging,
}))
const away = computed(() => awaySpans(props.blocks, from.value, to.value))
const spans = computed(() => priceSpans(props.slots, props.dates, props.advice, from.value, to.value))
const departures = computed(() => props.blocks.filter((b) => Date.parse(b.departAt) >= from.value))
const hasSolar = computed(() => props.solar.some((p) => p.kw > 0))

// The moment under the pointer or finger; null shows now.
const cursor = ref<number | null>(null)
const line = computed(() => readout(data.value, cursor.value ?? nowMs.value, cursor.value == null))
const summary = computed(() => chartSummary(data.value, props.dates, from.value, to.value))

type Series = 'soc' | 'required' | 'prices' | 'estimates' | 'charging' | 'solar' | 'away'
const hidden = reactive<Record<Series, boolean>>({
  soc: false,
  required: false,
  prices: false,
  estimates: false,
  charging: false,
  solar: false,
  away: false,
})
const legend = computed(() =>
  (
    [
      ['soc', 'Projected SoC', props.timeline.length > 0],
      ['required', 'Needed at departure', departures.value.length > 0],
      ['prices', 'Published prices', props.slots.length > 0],
      ['estimates', 'Estimated prices', spans.value.estimates.length > 0],
      ['charging', 'Expected charging', props.charging.length > 0],
      ['solar', 'Solar forecast', hasSolar.value],
      ['away', 'Away', away.value.length > 0],
    ] as [Series, string, boolean][]
  )
    .filter(([, , show]) => show)
    .map(([key, label]) => ({ key, label })),
)

function toggle(key: Series) {
  hidden[key] = !hidden[key]
  render()
}

const xy = (x: string | number, y: number | null) => ({ x: typeof x === 'number' ? x : Date.parse(x), y })

// One point at each slot start plus the last end, drawn as steps.
function steps(rates: Rate[]) {
  const pts = rates.map((r) => xy(r.start, r.value))
  const last = rates[rates.length - 1]
  if (last) pts.push(xy(last.end, last.value))
  return pts
}

// Separate flat pieces: each span, then a gap.
function pieces(list: { start: number; end: number; value: number }[]) {
  return list.flatMap((s) => [xy(s.start, s.value), xy(s.end, s.value), xy(s.end, null)])
}

const estimates = (basis: EstimateSpan['basis']) => spans.value.estimates.filter((s) => s.basis === basis)

const font = () => `600 11px ${cssVar('--font')}`

// Away periods (named on the SoC chart), days without prices, the "now" line,
// and the pointer line.
function bands(kind: 'soc' | 'price'): Plugin<'line'> {
  return {
    id: `bands-${kind}`,
    beforeDatasetsDraw(chart) {
      const { ctx, chartArea: area, scales } = chart
      const x = scales.x!
      ctx.save()
      ctx.font = font()
      ctx.textBaseline = 'top'
      if (!hidden.away) {
        for (const s of away.value) {
          const [x0, x1] = [x.getPixelForValue(s.start), x.getPixelForValue(s.end)]
          ctx.fillStyle = alpha(cssVar('--chart-away'), 0.14)
          ctx.fillRect(x0, area.top, x1 - x0, area.bottom - area.top)
          if (kind === 'soc' && ctx.measureText(s.label).width + 6 < x1 - x0) {
            ctx.fillStyle = cssVar('--chart-text')
            ctx.fillText(s.label, x0 + 3, area.top + 3)
          }
        }
      }
      if (kind === 'price') {
        ctx.fillStyle = cssVar('--chart-text')
        ctx.textAlign = 'center'
        ctx.textBaseline = 'middle'
        for (const s of spans.value.missing) {
          const [x0, x1] = [x.getPixelForValue(s.start), x.getPixelForValue(s.end)]
          const label = ctx.measureText('No price data').width + 6 < x1 - x0 ? 'No price data' : '–'
          ctx.fillText(label, (x0 + x1) / 2, (area.top + area.bottom) / 2)
        }
      }
      ctx.restore()
    },
    afterDatasetsDraw(chart) {
      const { ctx, chartArea: area, scales } = chart
      const x = scales.x!
      const mark = (t: number, color: string, dash: number[]) => {
        const px = x.getPixelForValue(t)
        if (px < area.left || px > area.right) return
        ctx.strokeStyle = color
        ctx.lineWidth = 1
        ctx.setLineDash(dash)
        ctx.beginPath()
        ctx.moveTo(px, area.top)
        ctx.lineTo(px, area.bottom)
        ctx.stroke()
      }
      ctx.save()
      mark(nowMs.value, cssVar('--chart-now'), [3, 3])
      if (cursor.value != null) mark(cursor.value, alpha(cssVar('--chart-now'), 0.6), [])
      ctx.restore()
    },
    afterEvent(chart, args) {
      const e = args.event
      if (e.type === 'mouseout') {
        cursor.value = null
        return
      }
      const area = chart.chartArea
      if (e.x == null || e.x < area.left || e.x > area.right) return
      cursor.value = chart.scales.x!.getValueForPixel(e.x) ?? null
    },
  }
}

// Options both charts share; only the price chart labels the time axis.
function common(kind: 'soc' | 'price', text: string, grid: string) {
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false as const,
    parsing: false as const,
    events: ['mousemove', 'mouseout', 'click', 'touchstart', 'touchmove'] as (
      'mousemove' | 'mouseout' | 'click' | 'touchstart' | 'touchmove'
    )[],
    layout: { padding: { top: kind === 'soc' ? 4 : 0 } },
    plugins: { legend: { display: false }, tooltip: { enabled: false } },
    scales: {
      x: {
        type: 'linear' as const,
        min: from.value,
        max: to.value,
        ticks: { display: kind === 'price', callback: timeTick, color: text, maxRotation: 0, autoSkip: true },
        afterBuildTicks: hourTicks(24),
        grid: { color: grid },
        border: { display: false },
      },
    },
  }
}

function socChart(canvas: HTMLCanvasElement) {
  const [text, grid] = [cssVar('--chart-text'), cssVar('--chart-grid')]
  const c = common('soc', text, grid)
  const soc = cssVar('--chart-soc')
  const required = cssVar('--chart-required')
  const datasets: ChartDataset<'line' | 'scatter', { x: number; y: number | null }[]>[] = [
    {
      type: 'line',
      label: 'Projected SoC',
      data: props.timeline.map((p) => xy(p.at, p.soc)),
      borderColor: soc,
      backgroundColor: alpha(soc, 0.13),
      fill: 'origin',
      pointRadius: 0,
      borderWidth: 2,
      hidden: hidden.soc,
    },
    {
      type: 'scatter',
      label: 'Needed at departure',
      data: departures.value.map((b) => xy(b.departAt, b.requiredSoc)),
      borderColor: required,
      backgroundColor: required,
      pointStyle: 'triangle',
      pointRadius: 5,
      hidden: hidden.required,
    },
    {
      type: 'line',
      label: 'Arrival reserve',
      data: [xy(from.value, props.reserve), xy(to.value, props.reserve)],
      borderColor: cssVar('--chart-reserve'),
      borderDash: [4, 4],
      borderWidth: 1,
      pointRadius: 0,
    },
  ]
  return new Chart(canvas, {
    type: 'line',
    data: { datasets },
    options: {
      ...c,
      scales: {
        ...c.scales,
        y: {
          min: 0,
          max: 100,
          ticks: { stepSize: 25, callback: (v) => `${v}%`, color: text },
          grid: { color: grid },
          border: { display: false },
          afterFit: (s) => void (s.width = AXIS_LEFT),
        },
        right: {
          position: 'right',
          ticks: { display: false },
          grid: { display: false },
          border: { display: false },
          afterFit: (s) => void (s.width = AXIS_RIGHT),
        },
      },
    },
    plugins: [bands('soc')],
  })
}

function priceChart(canvas: HTMLCanvasElement) {
  const [text, grid] = [cssVar('--chart-text'), cssVar('--chart-grid')]
  const c = common('price', text, grid)
  const charging = cssVar('--chart-charging')
  const solar = cssVar('--chart-solar')
  const datasets: ChartDataset<'line', { x: number; y: number | null }[]>[] = [
    {
      label: 'Published prices',
      data: steps(props.slots),
      stepped: 'before',
      borderColor: cssVar('--chart-price'),
      borderWidth: 1.5,
      pointRadius: 0,
      hidden: hidden.prices,
    },
    {
      label: 'Estimated day price',
      data: pieces(estimates('estimate')),
      borderColor: cssVar('--chart-estimate'),
      borderDash: [6, 4],
      borderWidth: 1.5,
      pointRadius: 0,
      spanGaps: false,
      hidden: hidden.estimates,
    },
    {
      label: 'Weekly average',
      data: pieces(estimates('weekly')),
      borderColor: cssVar('--chart-weekly'),
      borderDash: [2, 3],
      borderWidth: 1.5,
      pointRadius: 0,
      spanGaps: false,
      hidden: hidden.estimates,
    },
    {
      label: 'Expected charging',
      data: pieces(
        props.charging.map((r) => ({ start: Date.parse(r.start), end: Date.parse(r.end), value: r.value })),
      ),
      borderColor: charging,
      backgroundColor: alpha(charging, 0.4),
      borderWidth: 0,
      fill: 'origin',
      pointRadius: 0,
      spanGaps: false,
      hidden: hidden.charging,
    },
    {
      label: 'Solar forecast',
      data: props.solar.map((p) => xy(p.time, p.kw)),
      yAxisID: 'right',
      borderColor: solar,
      backgroundColor: alpha(solar, 0.15),
      borderWidth: 1.5,
      fill: 'origin',
      pointRadius: 0,
      hidden: hidden.solar || !hasSolar.value,
    },
  ]
  return new Chart(canvas, {
    type: 'line',
    data: { datasets },
    options: {
      ...c,
      scales: {
        ...c.scales,
        y: {
          beginAtZero: true,
          ticks: { maxTicksLimit: 4, callback: (v) => `€${Number(v).toFixed(2)}`, color: text },
          grid: { color: grid },
          border: { display: false },
          afterFit: (s) => void (s.width = AXIS_LEFT),
        },
        right: {
          position: 'right',
          min: 0,
          suggestedMax: 1,
          ticks: { display: hasSolar.value, maxTicksLimit: 3, callback: (v) => `${v} kW`, color: text },
          grid: { display: false },
          border: { display: false },
          afterFit: (s) => void (s.width = AXIS_RIGHT),
        },
      },
    },
    plugins: [bands('price')],
  })
}

function render() {
  for (const c of charts) c.destroy()
  charts = []
  if (socCanvas.value) charts.push(socChart(socCanvas.value))
  if (priceCanvas.value) charts.push(priceChart(priceCanvas.value))
}

let stopTheme = () => {}
onMounted(() => {
  render()
  stopTheme = onThemeChange(render)
})
watch(() => [props.timeline, props.blocks, props.slots, props.solar, props.charging, props.advice], render)
// The pointer line moves on both charts.
watch(cursor, () => charts.forEach((c) => c.draw()))
onUnmounted(() => {
  stopTheme()
  for (const c of charts) c.destroy()
})
</script>

<template>
  <div class="plan-chart">
    <p class="chart-readout small num" data-testid="chart-readout">{{ line }}</p>
    <div role="img" :aria-label="summary" data-testid="chart-summary">
      <div class="chart-soc"><canvas ref="socCanvas" /></div>
      <div class="chart-price"><canvas ref="priceCanvas" /></div>
    </div>
    <div class="chart-legend" role="group" aria-label="Show or hide">
      <button
        v-for="item in legend"
        :key="item.key"
        type="button"
        class="chip"
        :aria-pressed="!hidden[item.key]"
        :data-testid="`legend-${item.key}`"
        @click="toggle(item.key)"
      >
        <span class="swatch" :class="`swatch-${item.key}`" aria-hidden="true" />{{ item.label }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.plan-chart {
  display: grid;
  gap: var(--space-2);
}

.chart-readout {
  margin: 0;
  min-height: 1.45em;
  color: var(--text);
  white-space: normal;
}

.chart-soc,
.chart-price {
  position: relative;
}

.chart-soc {
  height: 150px;
}

.chart-price {
  height: 140px;
}

/* Horizontal drags move the readout; vertical ones still scroll the page. */
canvas {
  touch-action: pan-y;
}

.chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
}

/* Phones: one row that scrolls sideways instead of four rows of buttons. */
@media (max-width: 640px) {
  .chart-legend {
    flex-wrap: nowrap;
    overflow-x: auto;
    scrollbar-width: none;
    margin-inline: calc(var(--space-4) * -1);
    padding-inline: var(--space-4);
  }

  .chart-legend .chip {
    flex: none;
  }
}

.chart-legend .chip[aria-pressed='false'] {
  text-decoration: none;
}

.chart-legend .chip[aria-pressed='false'] .swatch {
  opacity: 0.35;
}

.swatch {
  width: 12px;
  height: 12px;
  border-radius: 3px;
  flex: none;
}

.swatch-soc {
  background: var(--chart-soc);
}

.swatch-required {
  background: var(--chart-required);
  clip-path: polygon(50% 0, 100% 100%, 0 100%);
}

.swatch-prices {
  background: var(--chart-price);
  height: 2px;
}

.swatch-estimates {
  border-top: 2px dashed var(--chart-estimate);
  height: 0;
}

.swatch-charging {
  background: var(--chart-charging);
}

.swatch-solar {
  background: var(--chart-solar);
}

.swatch-away {
  background: var(--chart-away);
  opacity: 0.35;
}
</style>
