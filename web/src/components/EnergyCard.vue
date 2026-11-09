<script setup lang="ts">
import type { Status } from '../api/types'
import { ago, kw, price } from '../lib/format'

defineProps<{ status: Status | null; now: string }>()
</script>

<template>
  <section class="card" aria-labelledby="energy-title">
    <div id="energy-title" class="card-title">Energy now</div>
    <dl class="kv">
      <dt>Grid price</dt>
      <dd>
        <strong>{{ price(status?.gridPrice) }}</strong> <span class="muted small">incl. VAT</span>
      </dd>
      <dt>Solar</dt>
      <dd>{{ kw(status?.pvPowerW ?? 0) }}</dd>
      <dt>Charger</dt>
      <dd>{{ status?.charger || '—' }}</dd>
      <dt>Data updated</dt>
      <dd>{{ status?.updatedAt ? ago(status.updatedAt, now) : 'never' }}</dd>
    </dl>
    <p v-if="status && !status.reachable" class="small text-bad" style="margin: 10px 0 0">
      EVCC unreachable{{ status.error ? `: ${status.error}` : '' }}. Showing the last known values.
    </p>
  </section>
</template>
