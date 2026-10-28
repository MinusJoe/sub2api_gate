import { computed, onBeforeUnmount, onMounted, readonly, ref } from 'vue'

export const PROXY_TEST_INTERVALS = [1, 3, 5, 10, 30, 60] as const
const STORAGE_KEY = 'admin:proxies:auto-test:interval'

export function isValidProxyTestInterval(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 1
}

/** Page-scoped polling: never schedules a second round while the first is running. */
export function useProxyAutoTest(options: {
  run: (signal: AbortSignal) => Promise<void>
  shouldPause: () => boolean
}) {
  const enabled = ref(false)
  const intervalSeconds = ref(30)
  const countdown = ref(30)
  const running = ref(false)
  const failed = ref(false)
  const hidden = ref(document.hidden)
  const paused = computed(() => hidden.value || options.shouldPause())
  let timer: ReturnType<typeof setInterval> | undefined
  let controller: AbortController | undefined
  let disposed = false

  // Remember the interval, but require an explicit start after reopening the page.
  try {
    const saved = Number(localStorage.getItem(STORAGE_KEY))
    if (isValidProxyTestInterval(saved)) intervalSeconds.value = saved
  } catch { /* Storage can be unavailable in private browsing. */ }
  countdown.value = intervalSeconds.value

  function restart() {
    controller?.abort()
    countdown.value = intervalSeconds.value
    failed.value = false
  }

  function setEnabled(value: boolean) {
    if (disposed) return
    enabled.value = value
    restart()
    if (timer !== undefined) clearInterval(timer)
    timer = value ? setInterval(() => { void tick() }, 1000) : undefined
  }

  function setIntervalSeconds(value: number) {
    if (!isValidProxyTestInterval(value)) return false
    intervalSeconds.value = value
    restart()
    try { localStorage.setItem(STORAGE_KEY, String(value)) } catch { /* optional */ }
    return true
  }

  async function tick() {
    if (!enabled.value || disposed || paused.value || running.value) return
    if (countdown.value > 1) {
      countdown.value--
      return
    }
    const current = new AbortController()
    controller = current
    running.value = true
    failed.value = false
    try {
      await options.run(current.signal)
    } catch {
      if (!current.signal.aborted) failed.value = true
    } finally {
      running.value = false
      if (controller === current) controller = undefined
      countdown.value = intervalSeconds.value
    }
  }

  function onVisibilityChange() {
    hidden.value = document.hidden
    restart()
  }
  function onPageHide() { setEnabled(false) }

  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibilityChange)
    window.addEventListener('pagehide', onPageHide)
  })
  onBeforeUnmount(() => {
    setEnabled(false)
    disposed = true
    document.removeEventListener('visibilitychange', onVisibilityChange)
    window.removeEventListener('pagehide', onPageHide)
  })

  return {
    enabled: readonly(enabled), intervalSeconds: readonly(intervalSeconds),
    countdown: readonly(countdown), running: readonly(running), failed: readonly(failed),
    paused, setEnabled, setIntervalSeconds, restart
  }
}
