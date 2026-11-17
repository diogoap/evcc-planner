<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api/client'
import type { History } from '../api/types'
import { historyChoice } from '../layout'
import { dayMonth, localDate } from '../lib/format'
import { periodFrom, periods, type HistorySeries } from '../lib/history'
import ErrorBanner from './ErrorBanner.vue'
import HistoryChart from './HistoryChart.vue'

// Each day's prices over a chosen period (a month by default), with wind,
// sun, driving, and the trips' energy on request. The planner saves each day as it starts and again at
// 23:45, so an hourly reload (and one when the period changes) is plenty.
const props = defineProps<{ now: string }>()

const history = ref<History | null>(null)
const error = ref('')
let request = 0

async function load() {
  const n = ++request
  try {
    const h = await api.history(periodFrom(historyChoice.period, localDate(props.now)))
    if (n === request) {
      history.value = h
      error.value = ''
    }
  } catch (e) {
    if (n === request) error.value = (e as Error).message
  }
}

const hour = computed(() => Math.floor(Date.parse(props.now) / 3_600_000))
watch(() => [historyChoice.period, hour.value], load, { immediate: true })

const legend: { key: HistorySeries; label: string }[] = [
  { key: 'range', label: 'Lowest to highest' },
  { key: 'average', label: 'Average' },
  { key: 'wind', label: 'Wind' },
  { key: 'sun', label: 'Sun' },
  { key: 'driving', label: 'Driving' },
  { key: 'energy', label: 'Energy' },
]
</script>

<template>
  <section class="card" aria-labelledby="history-title" data-testid="history-card">
    <div class="history-head">
      <div id="history-title" class="card-title m-0">
        Price history <span class="normal-case">incl. VAT</span>
      </div>
      <select
        v-model="historyChoice.period"
        class="period-select"
        aria-label="Period shown"
        data-testid="history-period"
      >
        <option v-for="p in periods" :key="p.key" :value="p.key">{{ p.label }}</option>
      </select>
    </div>
    <ErrorBanner
      v-if="error && !history"
      :message="`Couldn't load the price history: ${error}`"
      @retry="load"
    />
    <span v-else-if="!history" class="skeleton history-skeleton" aria-hidden="true" />
    <p v-else-if="!history.days.length" class="muted small m-0" data-testid="history-empty">
      Nothing recorded for this period yet. The planner saves each day's prices, driving, wind, and sun as the
      day starts and again at 23:45, from today on.
    </p>
    <template v-else>
      <HistoryChart :days="history.days" :today="history.today" :show="historyChoice.show" />
      <div class="chart-legend mt-2" role="group" aria-label="Show or hide">
        <button
          v-for="item in legend"
          :key="item.key"
          type="button"
          class="chip"
          :aria-pressed="historyChoice.show[item.key]"
          :data-testid="`history-legend-${item.key}`"
          @click="historyChoice.show[item.key] = !historyChoice.show[item.key]"
        >
          <span class="swatch" :class="`swatch-${item.key}`" aria-hidden="true" />{{ item.label }}
        </button>
      </div>
      <p class="small muted m-0 mt-3">
        One value per day, saved since {{ dayMonth(history.first ?? history.days[0]!.date) }}. Prices from
        EVCC; wind (Beaufort) and sun (share of the possible sunshine) from KNMI's forecast for the day;
        driving (km) and energy (kWh the trips need) from the day's trips.
      </p>
    </template>
  </section>
</template>

<style scoped>
.history-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.period-select {
  width: auto;
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-sm);
}

.history-skeleton {
  height: 260px;
}

.swatch-range {
  background: var(--chart-price);
  opacity: 0.3;
}

.swatch-average {
  background: var(--chart-price);
  height: 2px;
}

.swatch-wind {
  background: var(--chart-wind);
  height: 2px;
}

.swatch-sun {
  border-top: 2px dashed var(--chart-solar);
  height: 0;
}

.swatch-driving {
  background: var(--chart-away);
  opacity: 0.45;
}

.swatch-energy {
  background: var(--chart-required);
  height: 2px;
}
</style>
