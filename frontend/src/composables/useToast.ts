import { reactive } from 'vue'

export interface Toast {
  id: number
  kind: 'success' | 'error' | 'info'
  title: string
  body?: string
  details?: string[]
}

const state = reactive({ toasts: [] as Toast[] })
let seq = 0

export function useToast() {
  function dismiss(id: number) {
    const i = state.toasts.findIndex((t) => t.id === id)
    if (i >= 0) state.toasts.splice(i, 1)
  }
  function push(t: Omit<Toast, 'id'>, ttl = t.details?.length ? 12000 : 5000) {
    const id = ++seq
    state.toasts.push({ ...t, id })
    if (ttl > 0) setTimeout(() => dismiss(id), ttl)
    return id
  }
  function error(err: unknown, title = 'Something went wrong') {
    push({ kind: 'error', title, body: err instanceof Error ? err.message : String(err) })
  }
  return { state, push, dismiss, error }
}
