<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import ChartCards from '../components/ChartCards.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import PageSkeleton from '../components/PageSkeleton.vue'
import { layout } from '../layout'
import { refreshNow, state } from '../store'

// The week's charts on their own page: on phones, when the Overview with them
// would not fit the screen (the Charts button in the bottom bar). Tapping a
// day opens it in the Overview's day panel; back returns here.
const router = useRouter()
const plan = computed(() => state.plan)
const status = computed(() => state.status)

const box = ref<HTMLElement | null>(null)
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = window.ResizeObserver
    ? new ResizeObserver(() => {
        if (box.value?.offsetHeight) layout.charts = box.value.offsetHeight + 16
      })
    : undefined
  if (box.value) observer?.observe(box.value)
})
onUnmounted(() => observer?.disconnect())
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>Charts</h1></div>
    <ErrorBanner
      v-if="!plan && state.error"
      :message="`Couldn't load the plan: ${state.error}`"
      @retry="refreshNow"
    />
    <PageSkeleton v-else-if="!plan" kind="list" />
    <div v-else ref="box">
      <ChartCards :plan="plan" :status="status" @day="(day) => router.push({ path: '/', query: { day } })" />
    </div>
  </main>
</template>
