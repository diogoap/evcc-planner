<script setup lang="ts">
import { dayChoices, shownDays } from '../layout'

// How many days the Overview's week and the charts show.
</script>

<template>
  <select v-model.number="shownDays" class="days-select" aria-label="Days shown" data-testid="days-shown">
    <option v-for="n in dayChoices" :key="n" :value="n">{{ n }} days</option>
  </select>
</template>

<style scoped>
.days-select {
  width: auto;
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-sm);
}
</style>
