// Mirrors the Go API's JSON shapes.

export type Status =
  | 'received'
  | 'scheduled'
  | 'queued'
  | 'dispatched'
  | 'out_for_delivery'
  | 'completed'
  | 'cancelled'
  | 'rejected'

export type Source = 'webhook' | 'api' | 'csv'

export interface OrderSummary {
  id: number
  source: Source
  external_id: string
  channel: string
  restaurant: string
  customer_first: string
  customer_last: string
  item_count: number
  total: number | null
  status: Status
  external_status: string
  kind: 'realtime' | 'scheduled'
  meal: string
  scheduled_for: string | null
  flags: string[]
  needs_review: boolean
  notes: string
  created_at: string
  updated_at: string
  dispatched_at: string | null
}

export interface OrderList {
  items: OrderSummary[]
  total: number
  page: number
  page_size: number
}

export interface OrderItem {
  key: string
  name: string
  category: string
  price: number | null
  qty: number
  external_status: string
}

export interface OrderEvent {
  id: number
  type: string
  from_status: string
  to_status: string
  message: string
  payload: unknown
  created_at: string
}

export interface OrderDetail {
  order: OrderSummary
  items: OrderItem[]
  events: OrderEvent[]
  robot: { status: 'preview' | 'pending' | 'sent' | 'failed'; attempts: number; last_error: string; payload: unknown }
}

export interface DeadLetter {
  id: number
  source: string
  external_id: string
  reason: string
  detail: string
  payload: unknown
  order_id: number | null
  created_at: string
  resolved_at: string | null
}

export interface Related {
  conflicts: DeadLetter[]
  duplicates: { inbox_id: number; received_at: string; upload_id: number | null; payload: unknown }[]
  duplicate_count: number
}

export interface Stats {
  total: number
  by_status: Partial<Record<Status, number>>
  by_source: Partial<Record<Source, number>>
  needs_review: number
  inbox: { pending: number; retrying: number; dead: number }
  dead_letter_open: number
  throughput: { processed_last_min: number; received_last_min: number }
  outbox: { pending: number; failed: number }
  clock: { now: string; offset: string }
}

export interface ImportResult {
  upload_id: number
  filename: string
  rows: number
  new: number
  duplicate: number
  rejected: { line: number; reason: string; raw?: string }[]
}

export interface Ingestion {
  poller: {
    cursor: string
    last_status: string
    last_error: string
    leader: string
    last_polled_at: string | null
    consecutive_errors: number
  }
  leaders: { role: string; holder: string; since: string; heartbeat: string; alive: boolean }[]
  uploads: { id: number; filename: string; rows: number; new: number; duplicate: number; rejected: number; created_at: string }[]
  inbox: Record<string, Record<string, number>>
  mock_api: {
    lines?: number
    released?: number
    served?: number
    errors_served?: number
    step_seconds?: number
    robot?: { unique_messages: number; requests: number; injected_failures: number }
    error?: string
  } | null
}

export interface OrderQuery {
  status?: string
  source?: string
  kind?: string
  /** comma-separated; "needs_review" is a pseudo-flag for the needs_review column */
  flags?: string
  needs_review?: string
  q?: string
  sort?: string
  dir?: string
  page?: number
  page_size?: number
}
