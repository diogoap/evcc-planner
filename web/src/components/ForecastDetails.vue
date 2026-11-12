<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import { dateTime, dayShort } from '../lib/format'

interface Price {
  eurPerKWh: number
  provenance: 'stated' | 'converted'
  detail: string
  quote: string
}

interface Detail {
  outlook: {
    sourceUrl: string
    fetchedAt: string
    validFrom: string
    validTo: string
    extraction: string
    confidence: number
    weekly: { summary: string; trend: string; price: Price | null }
    days: { date: string; price: Price | null; level: string; note: string }[]
    warnings: string[]
  } | null
  weather: {
    source: string
    issuedAt: string | null
    summary: string
    days: { date: string }[]
  } | null
}

// The stored forecast payloads, so prices can be traced to the article.
const detail = ref<Detail | null>(null)

async function load() {
  detail.value = (await api.forecasts().catch(() => null)) as Detail | null
}
onMounted(load)
defineExpose({ load })
</script>

<template>
  <div v-if="detail" class="stack gap-3" data-testid="forecast-details">
    <div v-if="detail.outlook" class="small">
      <div class="card-title mb-1">Weekly price outlook</div>
      <p class="m-0 mb-2">
        Report {{ dayShort(detail.outlook.validFrom) }} – {{ dayShort(detail.outlook.validTo) }} · fetched
        {{ dateTime(detail.outlook.fetchedAt) }} · {{ detail.outlook.extraction }}
      </p>
      <p v-if="detail.outlook.weekly.summary" class="muted m-0 mb-2">
        {{ detail.outlook.weekly.summary }}
      </p>
      <ul class="list-indent">
        <li v-if="detail.outlook.weekly.price">
          Week: €{{ detail.outlook.weekly.price.eurPerKWh.toFixed(3) }}/kWh incl. VAT —
          {{ detail.outlook.weekly.price.detail }}
          <span class="muted">(“{{ detail.outlook.weekly.price.quote }}”)</span>
        </li>
        <li v-for="d in detail.outlook.days" :key="d.date">
          {{ dayShort(d.date) }}:
          <template v-if="d.price"
            >€{{ d.price.eurPerKWh.toFixed(3) }}/kWh incl. VAT ({{ d.price.provenance }}) —
            {{ d.price.detail }} <span class="muted">(“{{ d.price.quote }}”)</span></template
          >
          <template v-else>no price · {{ d.level.replace('_', ' ') }}</template>
        </li>
      </ul>
      <details v-if="detail.outlook.warnings.length" class="mt-2">
        <summary>{{ detail.outlook.warnings.length }} values dropped during validation</summary>
        <ul class="list-indent my-1">
          <li v-for="w in detail.outlook.warnings" :key="w" class="muted">{{ w }}</li>
        </ul>
      </details>
    </div>
    <div v-if="detail.weather" class="small">
      <div class="card-title mb-1">KNMI outlook</div>
      <p class="m-0">
        {{ detail.weather.days.length }} days from {{ dayShort(detail.weather.days[0]?.date ?? '') }}
        <template v-if="detail.weather.issuedAt"> · issued {{ dateTime(detail.weather.issuedAt) }}</template>
        · {{ detail.weather.summary }}
      </p>
    </div>
  </div>
</template>
