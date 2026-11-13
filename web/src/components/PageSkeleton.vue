<script setup lang="ts">
// Placeholders shaped like the page while it loads: the Overview's cards, a
// list of cards, or a form.
defineProps<{ kind: 'overview' | 'list' | 'form' }>()
</script>

<template>
  <div class="page-skeleton" aria-busy="true" data-testid="skeleton">
    <span class="sr-only" role="status">Loading…</span>
    <template v-if="kind === 'overview'">
      <div class="card stack gap-3">
        <span class="skeleton sk-line w30" />
        <span class="skeleton sk-big" />
        <span class="skeleton sk-bar" />
        <span class="skeleton sk-line w70" />
        <span class="skeleton sk-line w50" />
      </div>
      <div class="sk-week">
        <span v-for="i in 7" :key="i" class="skeleton sk-tile" />
      </div>
      <div class="card"><span class="skeleton sk-chart" /></div>
    </template>
    <template v-else-if="kind === 'list'">
      <div v-for="i in 3" :key="i" class="card stack gap-3">
        <span class="skeleton sk-line w35" />
        <span class="skeleton sk-line w85" />
        <span class="skeleton sk-line w60" />
      </div>
    </template>
    <div v-else class="card stack gap-3">
      <span class="skeleton sk-line w25" />
      <span v-for="i in 4" :key="i" class="skeleton sk-input" />
    </div>
  </div>
</template>

<style scoped>
.page-skeleton {
  display: grid;
  gap: var(--space-4);
}

.sk-line {
  height: 12px;
}

.w25 {
  width: 25%;
}

.w30 {
  width: 30%;
}

.w35 {
  width: 35%;
}

.w50 {
  width: 50%;
}

.w60 {
  width: 60%;
}

.w70 {
  width: 70%;
}

.w85 {
  width: 85%;
}

.sk-big {
  height: 36px;
  width: 30%;
}

.sk-bar {
  height: 10px;
}

.sk-input {
  height: 38px;
}

.sk-week {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: var(--space-2);
}

.sk-tile {
  height: 96px;
  border-radius: var(--radius-md);
}

.sk-chart {
  height: 220px;
}
</style>
