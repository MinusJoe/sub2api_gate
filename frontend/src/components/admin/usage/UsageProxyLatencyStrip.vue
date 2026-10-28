<template>
  <section
    class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700/50"
    :aria-label="t('usage.proxyLatency.title')"
    data-testid="usage-proxy-latency"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focusin="focused = true"
    @focusout="focused = ($event.currentTarget as HTMLElement).contains($event.relatedTarget as Node | null)"
  >
    <div class="shrink-0">
      <div class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-200">
        <Icon name="clock" size="sm" />
        {{ t('usage.proxyLatency.title') }}
        <span v-if="proxies.length" class="text-xs font-normal tabular-nums text-gray-500 dark:text-gray-400">{{ t('usage.proxyLatency.count', { count: proxies.length }) }}</span>
      </div>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('usage.proxyLatency.source') }}</p>
    </div>
    <div class="flex w-full min-w-0 flex-wrap items-center justify-end gap-3 sm:w-auto sm:flex-1">
      <div v-if="proxies.length" class="grid w-full min-w-0 grid-cols-2 gap-2 sm:w-[26rem]" data-testid="proxy-latency-pair">
        <div v-for="proxy in visibleProxies" :key="proxy.id" class="min-w-0 border-l border-gray-200 pl-3 dark:border-dark-600" :data-proxy-id="proxy.id">
          <div class="flex items-center justify-between gap-2">
            <span class="truncate text-sm text-gray-700 dark:text-gray-200" :title="proxy.name">{{ proxy.name }}</span>
            <span class="shrink-0 text-xs font-medium tabular-nums" :class="latencyClass(proxy)">{{ latencyText(proxy) }}</span>
          </div>
          <p class="mt-1 truncate text-[11px] text-gray-500 dark:text-gray-400" :title="sampleTime(proxy, true)">{{ sampleTime(proxy) }}</p>
        </div>
      </div>
      <span v-else class="text-xs text-gray-500 dark:text-gray-400" data-testid="proxy-latency-empty">
        {{ loading ? t('common.loading') : failed ? t('usage.proxyLatency.loadFailed') : targets.length ? t('usage.proxyLatency.missing') : t('usage.proxyLatency.empty') }}
      </span>
      <div class="flex shrink-0 items-center gap-1">
        <template v-if="pageCount > 1">
          <button type="button" class="rounded p-1.5 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" :aria-label="t('usage.proxyLatency.previous')" @click="changePage(-1)"><Icon name="arrowLeft" size="sm" /></button>
          <span class="w-10 text-center text-xs tabular-nums text-gray-500 dark:text-gray-400" data-testid="proxy-latency-page">{{ page + 1 }}/{{ pageCount }}</span>
          <button type="button" class="rounded p-1.5 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" :aria-label="t('usage.proxyLatency.next')" @click="changePage(1)"><Icon name="arrowRight" size="sm" /></button>
          <button type="button" class="rounded px-2 py-1.5 text-xs text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700" :aria-pressed="manualPause" @click="manualPause = !manualPause">{{ manualPause ? t('usage.proxyLatency.resume') : t('usage.proxyLatency.pause') }}</button>
        </template>
        <button type="button" class="rounded p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-40 dark:hover:bg-dark-700" :disabled="loading" :aria-label="t('usage.proxyLatency.refresh')" :title="t('usage.proxyLatency.refresh')" @click="load"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" /></button>
      </div>
    </div>
    <p v-if="failed && proxies.length" role="status" class="w-full text-xs text-amber-700 dark:text-amber-400">{{ t('usage.proxyLatency.stale') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { proxiesAPI } from '@/api/admin/proxies'
import Icon from '@/components/icons/Icon.vue'
import { PROXY_AUTO_TEST_TARGETS_KEY, readProxyAutoTestTargets } from '@/utils/proxyAutoTestTargets'
import type { Proxy } from '@/types'

type Summary = Pick<Proxy, 'id' | 'name' | 'latency_ms' | 'latency_status' | 'latency_checked_at'>
const { t, locale } = useI18n()
const proxies = ref<Summary[]>([])
const targets = ref<number[]>([])
const loading = ref(false)
const failed = ref(false)
const page = ref(0)
const hovered = ref(false)
const focused = ref(false)
const manualPause = ref(false)
const pageCount = computed(() => Math.ceil(proxies.value.length / 2))
const visibleProxies = computed(() => proxies.value.slice(page.value * 2, page.value * 2 + 2))
let controller: AbortController | undefined
let rotation: ReturnType<typeof setInterval> | undefined
let refresh: ReturnType<typeof setInterval> | undefined
let disposed = false

function changePage(delta: number) {
  if (pageCount.value > 0) page.value = (page.value + delta + pageCount.value) % pageCount.value
}
async function load() {
  if (disposed || document.hidden) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  const ids = readProxyAutoTestTargets()
  if (ids.join(',') !== targets.value.join(',')) {
    proxies.value = []
    page.value = 0
  }
  targets.value = ids
  failed.value = false
  loading.value = ids.length > 0
  if (!ids.length) { proxies.value = []; return }
  try {
    const remaining = new Set(ids)
    const found = new Map<number, Summary>()
    let currentPage = 1
    let pages = 1
    do {
      const response = await proxiesAPI.list(currentPage, 200, { sort_by: 'id', sort_order: 'asc' }, { signal: request.signal })
      if (request.signal.aborted || disposed) return
      for (const proxy of response.items) {
        if (!remaining.delete(proxy.id)) continue
        // Retain display fields only, never proxy credentials.
        found.set(proxy.id, { id: proxy.id, name: proxy.name, latency_ms: proxy.latency_ms, latency_status: proxy.latency_status, latency_checked_at: proxy.latency_checked_at })
      }
      pages = response.pages || 1
      currentPage++
    } while (remaining.size && currentPage <= pages)
    proxies.value = ids.flatMap(id => found.has(id) ? [found.get(id)!] : [])
    page.value = Math.min(page.value, Math.max(0, pageCount.value - 1))
  } catch {
    if (!request.signal.aborted && !disposed) failed.value = true
  } finally {
    if (controller === request) loading.value = false
  }
}
function latencyText(proxy: Summary) {
  if (proxy.latency_status === 'failed') return t('usage.proxyLatency.failed')
  return typeof proxy.latency_ms === 'number' && proxy.latency_ms >= 0
    ? `${proxy.latency_ms} ms` : t('usage.proxyLatency.untested')
}
function latencyClass(proxy: Summary) {
  if (proxy.latency_status === 'failed') return 'text-red-600 dark:text-red-400'
  if (typeof proxy.latency_ms !== 'number') return 'text-gray-500 dark:text-gray-400'
  return proxy.latency_ms < 200 ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'
}
function sampleTime(proxy: Summary, full = false) {
  if (!proxy.latency_checked_at) return t('usage.proxyLatency.noSampleTime')
  const date = new Date(proxy.latency_checked_at * 1000)
  const language = locale.value === 'zh' ? 'zh-CN' : 'en-US'
  const time = full ? date.toLocaleString(language) : date.toLocaleTimeString(language, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return t('usage.proxyLatency.sampledAt', { time })
}
function visibilityChanged() {
  if (document.hidden) controller?.abort()
  else void load()
}
function storageChanged(event: StorageEvent) {
  if (event.key === PROXY_AUTO_TEST_TARGETS_KEY || event.key === null) void load()
}
onMounted(() => {
  void load()
  rotation = setInterval(() => {
    if (!document.hidden && !hovered.value && !focused.value && !manualPause.value && pageCount.value > 1 && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) changePage(1)
  }, 5000)
  refresh = setInterval(() => { if (!loading.value) void load() }, 10000)
  document.addEventListener('visibilitychange', visibilityChanged)
  window.addEventListener('storage', storageChanged)
})
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  clearInterval(rotation)
  clearInterval(refresh)
  document.removeEventListener('visibilitychange', visibilityChanged)
  window.removeEventListener('storage', storageChanged)
})
</script>
