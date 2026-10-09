<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { OrderList, OrderQuery, OrderSummary, Stats } from '../api/types'
import DataTable, { type Column } from '../components/DataTable.vue'
import MultiSelect, { type MultiSelectOption } from '../components/MultiSelect.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { dataVersion } from '../composables/useActions'
import { usePolling } from '../composables/usePolling'
import {
  FILTER_KEYS,
  PAGE_SIZES,
  hasActiveFilters,
  loadStoredPageSize,
  parseList,
  resolvePageSize,
  serializeList,
  storePageSize,
} from '../filters'
import { FLAG_LABEL, SOURCE_LABEL, STATUS_LABEL, STATUS_ORDER, customer, fmtAgo, fmtMoney, fmtTime, label, shortId } from '../format'

const route = useRoute()
const router = useRouter()
const storedPageSize = ref(loadStoredPageSize())

// The URL is the source of truth for filters, so views are linkable and
// the header's "Download CSV" exports exactly what is on screen.
// Multi-value filters are comma-separated (?status=queued,dispatched).
const rq = computed(() => route.query as Record<string, string | undefined>)

// Legacy ?needs_review=true links show up as the "Needs review" flag.
const selectedFlags = computed(() => {
  const f = parseList(rq.value.flags)
  if (rq.value.needs_review === 'true' && !f.includes('needs_review')) f.unshift('needs_review')
  return f
})
const query = computed<OrderQuery>(() => {
  const q = rq.value
  return {
    status: q.status,
    source: q.source,
    kind: q.kind,
    flags: serializeList(selectedFlags.value),
    needs_review: q.needs_review === 'false' ? 'false' : undefined,
    q: q.q,
    sort: q.sort ?? 'id',
    dir: q.dir ?? 'desc',
    page: Math.max(1, Number(q.page ?? 1) || 1),
    page_size: resolvePageSize(q.page_size, storedPageSize.value),
  }
})
function setQuery(patch: Record<string, string | number | undefined>, resetPage = true) {
  const next: Record<string, string> = {}
  const merged = { ...route.query, ...patch, ...(resetPage ? { page: undefined } : {}) } as Record<string, unknown>
  for (const [k, v] of Object.entries(merged)) if (v !== undefined && v !== '' && v !== null) next[k] = String(v)
  router.replace({ query: next })
}

const list = ref<OrderList | null>(null)
const stats = ref<Stats | null>(null)
const loading = ref(false)
const live = ref(true)

async function load() {
  loading.value = true
  try {
    const [l, s] = await Promise.all([api.listOrders(query.value), api.stats()])
    list.value = l
    stats.value = s
  } finally {
    loading.value = false
  }
}
const { refresh, lastError } = usePolling(load, 5000, live)
watch([query, dataVersion], () => refresh())

const search = ref(rq.value.q ?? '')
let t: number | undefined
watch(search, (v) => {
  clearTimeout(t)
  if ((rq.value.q ?? '') === v.trim()) return
  t = window.setTimeout(() => setQuery({ q: v.trim() || undefined }), 300)
})
// A pending debounce must not rewrite the URL of the page we navigated to.
onBeforeUnmount(() => clearTimeout(t))
// Keep the box in sync when the URL changes underneath it (back/forward, Reset).
watch(
  () => rq.value.q,
  (v) => {
    if ((v ?? '') !== search.value.trim()) search.value = v ?? ''
  },
)

// ---- filter bar ----
const statusOptions = computed<MultiSelectOption[]>(() =>
  STATUS_ORDER.map((s) => ({
    value: s,
    label: STATUS_LABEL[s],
    count: stats.value ? (stats.value.by_status[s] ?? 0) : undefined,
  })),
)
const sourceOptions: MultiSelectOption[] = Object.entries(SOURCE_LABEL).map(([value, label]) => ({ value, label }))
const kindOptions: MultiSelectOption[] = [
  { value: 'realtime', label: 'Real-time' },
  { value: 'scheduled', label: 'Scheduled' },
]
const flagOptions: MultiSelectOption[] = [
  { value: 'needs_review', label: 'Needs review' },
  ...Object.entries(FLAG_LABEL).map(([value, label]) => ({ value, label })),
]

const selStatus = computed(() => parseList(rq.value.status))
const selSource = computed(() => parseList(rq.value.source))
const selKind = computed(() => parseList(rq.value.kind))

function setList(key: 'status' | 'source' | 'kind', values: string[]) {
  setQuery({ [key]: serializeList(values) })
}
function setFlags(values: string[]) {
  setQuery({ flags: serializeList(values), needs_review: undefined })
}

// Reset clears every filter and the search box, but keeps sort and page size.
const filtered = computed(() => hasActiveFilters(rq.value) || search.value.trim() !== '')
function resetFilters() {
  clearTimeout(t)
  search.value = ''
  setQuery(Object.fromEntries(FILTER_KEYS.map((k) => [k, undefined])))
}

function setPageSize(e: Event) {
  const n = Number((e.target as HTMLSelectElement).value)
  storePageSize(n)
  storedPageSize.value = n
  setQuery({ page_size: n })
}

const columns: Column[] = [
  { key: 'id', label: 'ID', sortable: true, width: '70px' },
  { key: 'source', label: 'Source', sortable: true },
  { key: 'customer', label: 'Customer', sortable: true },
  { key: 'restaurant', label: 'Restaurant' },
  { key: 'item_count', label: 'Items', align: 'right' },
  { key: 'total', label: 'Total', sortable: true, align: 'right' },
  { key: 'status', label: 'Status', sortable: true },
  { key: 'scheduled_for', label: 'When', sortable: true },
  { key: 'flags', label: 'Flags' },
  { key: 'updated_at', label: 'Updated', sortable: true, align: 'right' },
]
function onSort(key: string) {
  const dir = query.value.sort === key && query.value.dir === 'desc' ? 'asc' : 'desc'
  setQuery({ sort: key, dir }, false)
}
function open(o: OrderSummary, ev: MouseEvent) {
  if (ev.metaKey || ev.ctrlKey) window.open(`/orders/${o.id}`, '_blank')
  else router.push(`/orders/${o.id}`)
}

// Needs-review alert: large, always visible while any order is flagged.
const reviewCount = computed(() => stats.value?.needs_review ?? 0)
const showingReview = computed(() => selectedFlags.value.length === 1 && selectedFlags.value[0] === 'needs_review')
function toggleReviewFilter() {
  setFlags(showingReview.value ? [] : ['needs_review'])
}

const pages = computed(() => (list.value ? Math.max(1, Math.ceil(list.value.total / list.value.page_size)) : 1))
const now = computed(() => (list.value, Date.now()))
</script>

<template>
  <section v-if="reviewCount > 0" class="review-alert" role="status" aria-live="polite">
    <svg class="review-icon" viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4m0 4h.01" />
    </svg>
    <div class="review-text">
      <div class="review-title">
        <span class="num">{{ reviewCount.toLocaleString() }}</span> {{ reviewCount === 1 ? 'order needs' : 'orders need' }} review
      </div>
      <div class="review-sub">Flagged for a person to check: item-change requests, changed instructions, passed meal slots, ID conflicts. They still dispatch.</div>
    </div>
    <button class="btn review-btn" type="button" :aria-pressed="showingReview" @click="toggleReviewFilter">
      {{ showingReview ? 'Show all orders' : 'Show them' }}
    </button>
  </section>

  <div class="card">
    <div class="filters">
      <input v-model="search" class="input search" type="search" placeholder="Search name, order id, restaurant…" aria-label="Search orders" />
      <MultiSelect label="Status" :options="statusOptions" :model-value="selStatus" @update:model-value="setList('status', $event)" />
      <MultiSelect label="Source" :options="sourceOptions" :model-value="selSource" @update:model-value="setList('source', $event)" />
      <MultiSelect label="Type" :options="kindOptions" :model-value="selKind" @update:model-value="setList('kind', $event)" />
      <MultiSelect label="Flags" :options="flagOptions" :model-value="selectedFlags" @update:model-value="setFlags" />
      <button class="btn btn-ghost reset" type="button" :disabled="!filtered" @click="resetFilters">Reset</button>
      <span class="spacer" />
      <span v-if="lastError" class="err">{{ lastError }}</span>
      <button class="btn btn-sm live" :class="{ on: live }" type="button" :aria-pressed="live" @click="live = !live">
        <span class="live-dot" />{{ live ? 'Live' : 'Paused' }}
      </button>
    </div>

    <DataTable
      :columns="columns"
      :rows="list?.items ?? []"
      :row-key="(o) => o.id"
      :sort="query.sort"
      :dir="query.dir as 'asc' | 'desc'"
      :loading="loading && !list"
      clickable
      empty-text="No orders match these filters"
      @sort="onSort"
      @row-click="open"
    >
      <template #id="{ row }"><span class="mono muted">#{{ row.id }}</span></template>
      <template #source="{ row }">
        <div class="two">
          <span>{{ label(SOURCE_LABEL, row.source) }}</span>
          <span class="muted sub">{{ row.channel }} · <span class="mono">{{ shortId(row.external_id) }}</span></span>
        </div>
      </template>
      <template #customer="{ row }">{{ customer(row) }}</template>
      <template #restaurant="{ row }"><span :class="{ muted: !row.restaurant }">{{ row.restaurant || '—' }}</span></template>
      <template #item_count="{ row }"><span class="num">{{ row.item_count }}</span></template>
      <template #total="{ row }"><span class="num">{{ fmtMoney(row.total) }}</span></template>
      <template #status="{ row }"><StatusBadge :status="row.status" /></template>
      <template #scheduled_for="{ row }">
        <div v-if="row.kind === 'scheduled'" class="two">
          <span>{{ fmtTime(row.scheduled_for) }}</span>
          <span class="muted sub">{{ row.meal }}</span>
        </div>
        <span v-else class="muted">Real-time</span>
      </template>
      <template #flags="{ row }">
        <div class="flags">
          <span v-for="f in row.flags" :key="f" class="chip">{{ label(FLAG_LABEL, f) }}</span>
        </div>
      </template>
      <template #updated_at="{ row }"><span class="muted num" :title="fmtTime(row.updated_at, true)">{{ fmtAgo(row.updated_at, now) }}</span></template>
    </DataTable>

    <div class="pager">
      <span class="muted num">{{ list ? `${list.total.toLocaleString()} orders` : '' }}</span>
      <span class="spacer" />
      <label class="size">
        <span class="muted">Rows per page</span>
        <select class="select select-sm num" :value="query.page_size" @change="setPageSize">
          <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }}</option>
        </select>
      </label>
      <div class="nav">
        <button class="btn btn-sm" :disabled="query.page! <= 1" @click="setQuery({ page: query.page! - 1 }, false)">Previous</button>
        <span class="num">Page {{ query.page }} of {{ pages }}</span>
        <button class="btn btn-sm" :disabled="query.page! >= pages" @click="setQuery({ page: query.page! + 1 }, false)">Next</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.review-alert {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
  padding: 16px 20px;
  border: 1px solid color-mix(in srgb, var(--flag) 35%, transparent);
  border-left: 5px solid var(--flag);
  border-radius: var(--radius);
  background: var(--flag-bg);
  color: var(--text);
  box-shadow: var(--shadow);
}
.review-icon {
  flex: none;
  color: var(--flag);
}
.review-text {
  flex: 1;
  min-width: 0;
}
.review-title {
  font-size: 18px;
  font-weight: 650;
  letter-spacing: -0.01em;
}
.review-title .num {
  color: var(--flag);
}
.review-sub {
  margin-top: 2px;
  color: var(--text-2);
}
.review-btn {
  flex: none;
  border-color: color-mix(in srgb, var(--flag) 50%, transparent);
  font-weight: 600;
}
.review-btn[aria-pressed='true'],
.review-btn[aria-pressed='true']:hover:not(:disabled) {
  background: var(--flag);
  border-color: var(--flag);
  color: var(--surface);
}
@media (max-width: 640px) {
  .review-alert {
    flex-wrap: wrap;
    padding: 14px 16px;
  }
  .review-btn {
    width: 100%;
    justify-content: center;
  }
}
.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
}
.search {
  flex: 0 1 260px;
  min-width: 180px;
}
.reset {
  color: var(--text-2);
}
.reset:hover:not(:disabled) {
  color: var(--text);
}
.err {
  color: var(--danger);
  font-size: 12px;
}
.live {
  gap: 7px;
}
.live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-3);
}
.live.on .live-dot {
  background: var(--st-completed);
  animation: blink 2s infinite;
}
@keyframes blink {
  50% {
    opacity: 0.35;
  }
}
.two {
  display: flex;
  flex-direction: column;
  line-height: 1.3;
}
.sub {
  font-size: 12px;
}
.flags {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}
.pager {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px 16px;
  padding: 10px 12px;
  border-top: 1px solid var(--border);
}
.size,
.nav {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.select-sm {
  height: 26px;
  padding: 0 4px 0 7px;
  font-size: 12.5px;
}
@media (max-width: 640px) {
  .search {
    flex-basis: 100%;
  }
  .filters > .ms {
    flex: 1 1 calc(50% - 4px);
  }
  .filters > .ms :deep(.ms-btn) {
    width: 100%;
    max-width: none;
    justify-content: space-between;
  }
  .pager .spacer {
    display: none;
  }
}
</style>
