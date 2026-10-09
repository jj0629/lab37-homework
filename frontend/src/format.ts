import type { OrderSummary, Status } from './api/types'

export const STATUS_ORDER: Status[] = [
  'received',
  'scheduled',
  'queued',
  'dispatched',
  'out_for_delivery',
  'completed',
  'cancelled',
  'rejected',
]

export const STATUS_LABEL: Record<string, string> = {
  received: 'Received',
  scheduled: 'Scheduled',
  queued: 'Queued',
  dispatched: 'Dispatched',
  out_for_delivery: 'Out for delivery',
  completed: 'Completed',
  cancelled: 'Cancelled',
  rejected: 'Rejected',
}

export const SOURCE_LABEL: Record<string, string> = { webhook: 'Webhook', api: 'Polling API', csv: 'CSV upload' }

export const FLAG_LABEL: Record<string, string> = {
  slot_passed_rescheduled: 'Slot passed',
  item_change_requested: 'Item change request',
  instructions_changed: 'Changed mind',
  id_conflict: 'ID conflict',
  allergy: 'Allergy',
  unknown_item: 'Unknown item',
  cancel_after_dispatch: 'Cancel after dispatch',
  robot_delivery_failed: 'Robot unreachable',
}

export function label(map: Record<string, string>, key: string): string {
  return map[key] ?? key.replace(/_/g, ' ')
}

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })
export function fmtMoney(v: number | null | undefined): string {
  return v == null ? '—' : money.format(v)
}

const dtf = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
const dtfSec = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
  second: '2-digit',
})
export function fmtTime(iso: string | null | undefined, seconds = false): string {
  if (!iso) return '—'
  return (seconds ? dtfSec : dtf).format(new Date(iso))
}

export function fmtAgo(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '—'
  const s = Math.round((now - new Date(iso).getTime()) / 1000)
  const abs = Math.abs(s)
  const unit = abs < 60 ? [abs, 's'] : abs < 3600 ? [Math.round(abs / 60), 'm'] : abs < 86400 ? [Math.round(abs / 3600), 'h'] : [Math.round(abs / 86400), 'd']
  return s >= 0 ? `${unit[0]}${unit[1]} ago` : `in ${unit[0]}${unit[1]}`
}

export function customer(o: Pick<OrderSummary, 'customer_first' | 'customer_last'>): string {
  return `${o.customer_first} ${o.customer_last}`.trim() || '—'
}

export function shortId(id: string): string {
  return id.length > 14 ? id.slice(0, 8) + '…' : id
}
