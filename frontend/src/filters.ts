// Pure helpers for the orders filter bar: URL (de)serialisation of
// multi-value filters, the MultiSelect button summary, and the remembered
// page size. Kept free of Vue so they are trivially unit-testable.

/** Query keys the filter bar owns. Reset clears exactly these. */
export const FILTER_KEYS = ['status', 'source', 'kind', 'flags', 'needs_review', 'q'] as const
export type FilterKey = (typeof FILTER_KEYS)[number]

export const PAGE_SIZES = [10, 25, 50, 100] as const
export const DEFAULT_PAGE_SIZE = 50
export const PAGE_SIZE_STORAGE_KEY = 'orders.pageSize'

type QueryValue = string | null | undefined | (string | null)[]

/** "a,b,,c" (or a repeated ?k=a&k=b) -> ["a","b","c"], de-duplicated, order kept. */
export function parseList(v: QueryValue): string[] {
  const raw = Array.isArray(v) ? v.join(',') : (v ?? '')
  const out: string[] = []
  for (const part of raw.split(',')) {
    const s = part.trim()
    if (s && !out.includes(s)) out.push(s)
  }
  return out
}

/** ["a","b"] -> "a,b"; [] -> undefined (so the key is dropped from the URL). */
export function serializeList(values: readonly string[]): string | undefined {
  const clean = parseList(values.join(','))
  return clean.length ? clean.join(',') : undefined
}

/** True when any filter (including the search box) narrows the list. */
export function hasActiveFilters(query: Record<string, QueryValue>): boolean {
  return FILTER_KEYS.some((k) => parseList(query[k]).length > 0)
}

/**
 * Button text for a multi-select:
 *   []                 -> "Status"
 *   ["Dispatched"]     -> "Status: Dispatched"
 *   two that fit       -> "Status: Dispatched, Completed"
 *   otherwise          -> "Status: 3 selected"
 */
export function summarizeSelection(label: string, selectedLabels: readonly string[], maxChars = 24): string {
  const n = selectedLabels.length
  if (n === 0) return label
  if (n === 1) return `${label}: ${selectedLabels[0]}`
  const joined = selectedLabels.join(', ')
  if (n === 2 && joined.length <= maxChars) return `${label}: ${joined}`
  return `${label}: ${n} selected`
}

export function normalizePageSize(v: unknown): number | undefined {
  const n = Number(v)
  return (PAGE_SIZES as readonly number[]).includes(n) ? n : undefined
}

/** URL wins, then the remembered default, then 50. */
export function resolvePageSize(fromUrl: unknown, stored: unknown): number {
  return normalizePageSize(fromUrl) ?? normalizePageSize(stored) ?? DEFAULT_PAGE_SIZE
}

export function loadStoredPageSize(): number | undefined {
  try {
    return normalizePageSize(window.localStorage.getItem(PAGE_SIZE_STORAGE_KEY))
  } catch {
    return undefined
  }
}

export function storePageSize(n: number): void {
  try {
    window.localStorage.setItem(PAGE_SIZE_STORAGE_KEY, String(n))
  } catch {
    // storage unavailable (private mode, blocked) - the URL still carries it
  }
}
