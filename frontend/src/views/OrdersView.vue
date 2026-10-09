<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { OrderList, OrderQuery, OrderSummary, Stats } from '../api/types'
import DataTable, { type Column } from '../components/DataTable.vue'
import StatTile from '../components/StatTile.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { dataVersion } from '../composables/useActions'
import { usePolling } from '../composables/usePolling'
import { FLAG_LABEL, SOURCE_LABEL, customer, fmtAgo, fmtMoney, fmtTime, label, shortId } from '../format'

const route = useRoute()
const router = useRouter()

// The URL is the source of truth for filters, so views are linkable and
// the header's "Download CSV" exports exactly what is on screen.
const query = computed<OrderQuery>(() => {
  const q = route.query as Record<string, string | undefined>
  return {
    status: q.status,
    source: q.source,
    kind: q.kind,
    needs_review: q.needs_review,
    q: q.q,
    sort: q.sort ?? 'id',
    dir: q.dir ?? 'desc',
    page: Number(q.page ?? 1),
    page_size: 50,
  }
})
function setQuery(patch: Partial<Record<keyof OrderQuery, string | number | undefined>>, resetPage = true) {
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

const search = ref((route.query.q as string) ?? '')
let t: number | undefined
watch(search, (v) => {
  clearTimeout(t)
  t = window.setTimeout(() => setQuery({ q: v.trim() || undefined }), 300)
})

const tabs = computed(() => {
  const by = stats.value?.by_status ?? {}
  return [
    { key: '', label: 'All', n: stats.value?.total ?? 0 },
    { key: 'scheduled', label: 'Scheduled', n: by.scheduled ?? 0 },
    { key: 'queued', label: 'Queued', n: by.queued ?? 0 },
    { key: 'dispatched', label: 'Dispatched', n: by.dispatched ?? 0 },
    { key: 'out_for_delivery', label: 'Out for delivery', n: by.out_for_delivery ?? 0 },
    { key: 'completed', label: 'Completed', n: by.completed ?? 0 },
    { key: 'cancelled', label: 'Cancelled', n: by.cancelled ?? 0 },
    { key: 'rejected', label: 'Rejected', n: by.rejected ?? 0 },
  ]
})

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

const pages = computed(() => (list.value ? Math.max(1, Math.ceil(list.value.total / list.value.page_size)) : 1))
const now = computed(() => (list.value, Date.now()))
const scheduledNote = computed(() => {
  const s = stats.value
  return s ? `${s.by_status.scheduled ?? 0} waiting for a meal slot` : ''
})
</script>

<template>
  <section class="tiles">
    <StatTile label="All orders" :value="stats?.total ?? '—'" :hint="`${stats?.by_source.webhook ?? 0} webhook · ${stats?.by_source.api ?? 0} API · ${stats?.by_source.csv ?? 0} CSV`" />
    <StatTile label="Scheduled" tone="scheduled" :value="stats?.by_status.scheduled ?? 0" :hint="scheduledNote" clickable :active="query.status === 'scheduled'" @click="setQuery({ status: query.status === 'scheduled' ? undefined : 'scheduled' })" />
    <StatTile label="Queued" tone="queued" :value="stats?.by_status.queued ?? 0" hint="waiting for robot" clickable :active="query.status === 'queued'" @click="setQuery({ status: query.status === 'queued' ? undefined : 'queued' })" />
    <StatTile label="Dispatched" tone="dispatched" :value="stats?.by_status.dispatched ?? 0" :hint="`${stats?.outbox.pending ?? 0} robot msgs pending`" clickable :active="query.status === 'dispatched'" @click="setQuery({ status: query.status === 'dispatched' ? undefined : 'dispatched' })" />
    <StatTile label="Out for delivery" tone="out_for_delivery" :value="stats?.by_status.out_for_delivery ?? 0" clickable :active="query.status === 'out_for_delivery'" @click="setQuery({ status: query.status === 'out_for_delivery' ? undefined : 'out_for_delivery' })" />
    <StatTile label="Completed" tone="completed" :value="stats?.by_status.completed ?? 0" clickable :active="query.status === 'completed'" @click="setQuery({ status: query.status === 'completed' ? undefined : 'completed' })" />
    <StatTile label="Needs review" tone="queued" :value="stats?.needs_review ?? 0" hint="flagged, not blocked" clickable :active="query.needs_review === 'true'" @click="setQuery({ needs_review: query.needs_review === 'true' ? undefined : 'true' })" />
    <StatTile label="Inbox backlog" tone="received" :value="stats?.inbox.pending ?? 0" :hint="`${stats?.throughput.processed_last_min ?? 0} processed / min`" />
  </section>

  <div class="card">
    <div class="tabs" role="tablist">
      <button
        v-for="t in tabs"
        :key="t.key"
        role="tab"
        class="tab"
        :class="{ on: (query.status ?? '') === t.key }"
        :aria-selected="(query.status ?? '') === t.key"
        @click="setQuery({ status: t.key || undefined })"
      >
        {{ t.label }} <span class="n num">{{ t.n }}</span>
      </button>
    </div>
    <div class="filters">
      <input v-model="search" class="input search" type="search" placeholder="Search name, order id, restaurant…" aria-label="Search" />
      <select class="select" :value="query.source ?? ''" aria-label="Source" @change="setQuery({ source: ($event.target as HTMLSelectElement).value || undefined })">
        <option value="">All sources</option>
        <option value="webhook">Webhook</option>
        <option value="api">Polling API</option>
        <option value="csv">CSV upload</option>
      </select>
      <select class="select" :value="query.kind ?? ''" aria-label="Kind" @change="setQuery({ kind: ($event.target as HTMLSelectElement).value || undefined })">
        <option value="">Real-time + scheduled</option>
        <option value="realtime">Real-time</option>
        <option value="scheduled">Scheduled</option>
      </select>
      <label class="check">
        <input type="checkbox" :checked="query.needs_review === 'true'" @change="setQuery({ needs_review: ($event.target as HTMLInputElement).checked ? 'true' : undefined })" />
        Needs review
      </label>
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
      <button class="btn btn-sm" :disabled="query.page! <= 1" @click="setQuery({ page: query.page! - 1 }, false)">Previous</button>
      <span class="num">Page {{ query.page }} of {{ pages }}</span>
      <button class="btn btn-sm" :disabled="query.page! >= pages" @click="setQuery({ page: query.page! + 1 }, false)">Next</button>
    </div>
  </div>
</template>

<style scoped>
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(128px, 1fr));
  gap: 10px;
  margin-bottom: 16px;
}
.tabs {
  display: flex;
  gap: 2px;
  padding: 0 10px;
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
}
.tab {
  height: 40px;
  padding: 0 10px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--text-2);
  font: inherit;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
}
.tab:hover {
  color: var(--text);
}
.tab.on {
  color: var(--accent);
  border-bottom-color: var(--accent);
}
.tab .n {
  margin-left: 3px;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--surface-2);
  color: var(--text-3);
  font-size: 11px;
}
.tab.on .n {
  background: var(--accent-soft);
  color: var(--accent);
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
  width: 260px;
  max-width: 100%;
}
.check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-2);
  cursor: pointer;
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
  gap: 10px;
  padding: 10px 12px;
  border-top: 1px solid var(--border);
}
</style>
