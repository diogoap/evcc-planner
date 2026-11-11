<script setup lang="ts">
import { computed } from 'vue'
import type { SoCSource, SoCView, Status } from '../api/types'
import { ago, dateTime, km, kw, pct } from '../lib/format'

const props = defineProps<{ soc: SoCView; status: Status | null; now: string; target?: number }>()

const sourceName: Record<SoCSource, string> = { evcc: 'EVCC', homeassistant: 'Home Assistant' }

const percent = computed(() => props.soc.percent ?? 0)
const fromHA = computed(() => props.soc.source === 'homeassistant')
/** Where the reading comes from and how old it is, for a fresh reading. */
const sourceLine = computed(() => {
  const age = props.soc.observedAt ? ago(props.soc.observedAt, props.now) : ''
  if (props.soc.live) return `Live from EVCC · ${age}`
  if (fromHA.value) return `From Home Assistant · ${age}`
  return `From EVCC · ${age}, read before the car was unplugged`
})
const staleHint = computed(() =>
  fromHA.value
    ? 'Home Assistant has not updated it since.'
    : 'EVCC only reads the SoC while the car is plugged in.',
)
</script>

<template>
  <section class="card" aria-labelledby="vehicle-title">
    <div id="vehicle-title" class="card-title">{{ status?.vehicleTitle || 'Vehicle' }}</div>
    <template v-if="soc.known">
      <div class="row">
        <span class="big" data-testid="soc">{{ pct(soc.percent) }}</span>
        <span v-if="soc.rangeKm != null" class="muted" data-testid="soc-range"
          >{{ km(soc.rangeKm) }} range</span
        >
        <span
          v-else-if="fromHA"
          class="muted small"
          data-testid="soc-range"
          title="Choose a range sensor under Settings → Connections to read it from Home Assistant"
          >Range: not reported</span
        >
        <span
          v-else
          class="muted small"
          data-testid="soc-range"
          title="EVCC reports the range only while the car is plugged in"
          >Range: read when plugged in</span
        >
        <span v-if="soc.estimated && soc.projected != null" class="muted small">
          ≈ {{ pct(soc.projected) }} after trips since
        </span>
      </div>
      <div class="bar" style="margin: 10px 0" role="img" :aria-label="`State of charge ${pct(soc.percent)}`">
        <div class="bar-fill" :class="{ stale: soc.stale }" :style="{ width: `${percent}%` }" />
        <div v-if="target" class="bar-target" :style="{ left: `${Math.min(target, 100)}%` }" title="Target" />
      </div>
      <p v-if="soc.stale" class="small text-warn" style="margin: 0 0 8px" data-testid="soc-stale">
        <span aria-hidden="true">ⓘ</span>
        Last known, {{ soc.observedAt ? dateTime(soc.observedAt) : '' }}, from
        {{ fromHA ? 'Home Assistant' : 'EVCC' }}
        <span class="muted">· {{ staleHint }}</span>
      </p>
      <p v-else-if="soc.observedAt" class="small muted" style="margin: 0 0 8px" data-testid="soc-source">
        {{ sourceLine }}
      </p>
      <p
        v-for="o in soc.other"
        :key="o.source"
        class="small muted"
        style="margin: 0 0 8px"
        data-testid="soc-other"
      >
        {{ sourceName[o.source] }}: {{ pct(o.percent) }} · {{ ago(o.observedAt, now) }}
      </p>
    </template>
    <template v-else>
      <div class="big muted">—</div>
      <p class="small text-warn">SoC unknown. Plug in the car so EVCC can read it.</p>
    </template>
    <dl class="kv">
      <dt>Connection</dt>
      <dd>
        <span :class="status?.connected ? 'text-ok' : 'muted'">{{
          status?.connected ? 'Plugged in' : 'Unplugged'
        }}</span>
      </dd>
      <dt>Charging</dt>
      <dd>{{ kw(status?.chargePowerW ?? 0) }}</dd>
    </dl>
  </section>
</template>
