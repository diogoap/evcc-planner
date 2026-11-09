<script setup lang="ts">
import { computed } from 'vue'
import type { SoCView, Status } from '../api/types'
import { ago, dateTime, kw, pct } from '../lib/format'

const props = defineProps<{ soc: SoCView; status: Status | null; now: string; target?: number }>()

const percent = computed(() => props.soc.percent ?? 0)
const stale = computed(() => props.soc.known && !props.soc.live)
</script>

<template>
  <section class="card" aria-labelledby="vehicle-title">
    <div id="vehicle-title" class="card-title">{{ status?.vehicleTitle || 'Vehicle' }}</div>
    <template v-if="soc.known">
      <div class="row">
        <span class="big" data-testid="soc">{{ pct(soc.percent) }}</span>
        <span v-if="soc.estimated && soc.projected != null" class="muted small">
          ≈ {{ pct(soc.projected) }} after trips since
        </span>
      </div>
      <div class="bar" style="margin: 10px 0" role="img" :aria-label="`State of charge ${pct(soc.percent)}`">
        <div class="bar-fill" :class="{ stale }" :style="{ width: `${percent}%` }" />
        <div v-if="target" class="bar-target" :style="{ left: `${Math.min(target, 100)}%` }" title="Target" />
      </div>
      <p v-if="stale" class="small text-warn" style="margin: 0 0 8px" data-testid="soc-stale">
        <span aria-hidden="true">ⓘ</span>
        Last known, {{ soc.observedAt ? dateTime(soc.observedAt) : '' }}
        <span class="muted">· EVCC only reads the SoC while the car is plugged in.</span>
      </p>
      <p v-else-if="soc.observedAt" class="small muted" style="margin: 0 0 8px">
        Live from EVCC · {{ ago(soc.observedAt, now) }}
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
