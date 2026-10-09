import type {
  DeadLetter,
  ImportResult,
  Ingestion,
  OrderDetail,
  OrderList,
  OrderQuery,
  Related,
  Stats,
} from './types'

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: {} }
  if (body instanceof FormData) {
    init.body = body
  } else if (body !== undefined) {
    init.body = JSON.stringify(body)
    init.headers = { 'Content-Type': 'application/json' }
  }
  let res: Response
  try {
    res = await fetch('/api' + path, init)
  } catch {
    throw new ApiError('Cannot reach the API server', 0)
  }
  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: text }
    }
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | null)?.error ?? res.statusText
    throw new ApiError(msg, res.status)
  }
  return data as T
}

export function queryString(q: OrderQuery): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v !== undefined && v !== null && v !== '') p.set(k, String(v))
  }
  const s = p.toString()
  return s ? '?' + s : ''
}

export const api = {
  listOrders: (q: OrderQuery) => request<OrderList>('GET', '/orders' + queryString(q)),
  getOrder: (id: number) => request<OrderDetail>('GET', `/orders/${id}`),
  getRelated: (id: number) => request<Related>('GET', `/orders/${id}/related`),
  dispatch: (id: number) => request<{ status: string }>('POST', `/orders/${id}/dispatch`),
  cancel: (id: number, reason: string) => request<{ result: string }>('POST', `/orders/${id}/cancel`, { reason }),
  exportUrl: (q: OrderQuery) => '/api/orders/export.csv' + queryString({ ...q, page: undefined, page_size: undefined }),

  stats: () => request<Stats>('GET', '/stats'),
  deadLetter: (includeResolved = false) =>
    request<DeadLetter[]>('GET', '/dead-letter' + (includeResolved ? '?include_resolved=true' : '')),
  resolveDeadLetter: (id: number) => request<unknown>('POST', `/dead-letter/${id}/resolve`),

  importCSV: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return request<ImportResult>('POST', '/orders/import', fd)
  },
  ingestion: () => request<Ingestion>('GET', '/ingestion'),
  samples: () => request<{ name: string; bytes: number }[]>('GET', '/admin/samples'),
  importSample: (file: string) => request<ImportResult>('POST', '/admin/import-sample', { file }),
  injectWebhook: (body: { count: number; fresh: boolean; offset?: number }) =>
    request<{ accepted: number; duplicates: number; sample_lines: number }>('POST', '/admin/inject-webhook', body),
  pollNow: () => request<unknown>('POST', '/admin/poll-now'),
  timeWarp: (body: { minutes?: number; to_next_release?: boolean; reset?: boolean }) =>
    request<{ now: string; offset: string }>('POST', '/admin/time-warp', body),
  reset: () => request<unknown>('POST', '/admin/reset'),
}
