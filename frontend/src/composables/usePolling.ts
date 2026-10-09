import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

/**
 * Calls fn now and every `ms` while `enabled` is true and the tab is visible.
 * Overlapping calls are skipped so a slow request never piles up.
 */
export function usePolling(fn: () => Promise<void>, ms: number, enabled: Ref<boolean> = ref(true)) {
  let timer: number | undefined
  let busy = false
  const lastError = ref<string | null>(null)

  async function tick() {
    if (busy) return
    busy = true
    try {
      await fn()
      lastError.value = null
    } catch (e) {
      lastError.value = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }
  function start() {
    stop()
    if (enabled.value) timer = window.setInterval(() => !document.hidden && tick(), ms)
  }
  function stop() {
    if (timer) window.clearInterval(timer)
    timer = undefined
  }
  const onVisible = () => !document.hidden && enabled.value && tick()

  onMounted(() => {
    tick()
    start()
    document.addEventListener('visibilitychange', onVisible)
  })
  onBeforeUnmount(() => {
    stop()
    document.removeEventListener('visibilitychange', onVisible)
  })
  watch(enabled, start)

  return { refresh: tick, lastError }
}
