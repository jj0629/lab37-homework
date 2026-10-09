<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, ApiError } from '../api/client'
import type { OrderDetail, Related } from '../api/types'
import JsonViewer from '../components/JsonViewer.vue'
import StatusBadge from '../components/StatusBadge.vue'
import Timeline from '../components/Timeline.vue'
import { bump, dataVersion } from '../composables/useActions'
import { usePolling } from '../composables/usePolling'
import { useToast } from '../composables/useToast'
import { FLAG_LABEL, SOURCE_LABEL, customer, fmtMoney, fmtTime, label } from '../format'

const props = defineProps<{ id: number }>()
const toast = useToast()

const detail = ref<OrderDetail | null>(null)
const related = ref<Related | null>(null)
const notFound = ref(false)
const busy = ref(false)

async function load() {
  try {
    const [d, r] = await Promise.all([api.getOrder(props.id), api.getRelated(props.id)])
    detail.value = d
    related.value = r
    notFound.value = false
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else throw e
  }
}
const { refresh } = usePolling(load, 5000)
watch(
  () => props.id,
  () => {
    detail.value = null
    related.value = null
    refresh()
  },
)
watch(dataVersion, () => refresh())

const o = computed(() => detail.value?.order)
const canDispatch = computed(() => o.value && (o.value.status === 'received' || o.value.status === 'scheduled'))
const canCancel = computed(() => o.value && !['completed', 'cancelled', 'rejected'].includes(o.value.status))
const itemsTotal = computed(() =>
  (detail.value?.items ?? []).reduce((s, it) => s + (it.price ?? 0) * it.qty, 0),
)
const hasPrices = computed(() => (detail.value?.items ?? []).some((it) => it.price != null))
const events = computed(() => [...(detail.value?.events ?? [])].reverse())

async function dispatchNow() {
  busy.value = true
  try {
    await api.dispatch(props.id)
    toast.push({ kind: 'success', title: `Order #${props.id} sent to the dispatch queue` })
    bump()
  } catch (e) {
    toast.error(e, 'Could not dispatch')
  } finally {
    busy.value = false
  }
}
const confirming = ref(false)
const reason = ref('')
async function cancel() {
  busy.value = true
  try {
    const r = await api.cancel(props.id, reason.value.trim() || 'cancelled by operator')
    confirming.value = false
    reason.value = ''
    toast.push({
      kind: 'success',
      title: r.result === 'cancel_requested' ? 'Cancel sent to robot' : `Order #${props.id} cancelled`,
      body: r.result === 'cancel_requested' ? 'The order was already dispatched; the robot has been told to stop.' : undefined,
    })
    bump()
  } catch (e) {
    toast.error(e, 'Could not cancel')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div v-if="notFound" class="card empty">
    Order #{{ id }} does not exist. <RouterLink to="/orders">Back to orders</RouterLink>
  </div>
  <div v-else-if="!detail || !o" class="card empty">Loading order…</div>
  <template v-else>
    <RouterLink to="/orders" class="back">← All orders</RouterLink>

    <section class="card head">
      <div class="title-row">
        <h2>Order #{{ o.id }}</h2>
        <StatusBadge :status="o.status" />
        <span v-if="o.external_status" class="chip chip-neutral" title="Raw upstream status">upstream: {{ o.external_status }}</span>
        <span v-for="f in o.flags" :key="f" class="chip">{{ label(FLAG_LABEL, f) }}</span>
        <span class="spacer" />
        <button class="btn" :disabled="!canDispatch || busy" :title="canDispatch ? 'Release to the robot now' : 'Only received or scheduled orders can be dispatched early'" @click="dispatchNow">Dispatch now</button>
        <button v-if="!confirming" class="btn btn-danger" :disabled="!canCancel || busy" @click="confirming = true">Cancel order</button>
      </div>
      <form v-if="confirming" class="confirm" @submit.prevent="cancel">
        <span>{{ o.status === 'dispatched' || o.status === 'out_for_delivery' ? 'Already with the robot: a cancel message will be sent.' : 'Stop this order before it reaches the robot?' }}</span>
        <input v-model="reason" class="input" placeholder="Reason (optional)" aria-label="Cancel reason" />
        <button class="btn btn-danger" type="submit" :disabled="busy">Confirm cancel</button>
        <button class="btn btn-ghost" type="button" @click="confirming = false">Keep order</button>
      </form>
      <div class="facts">
        <dl class="kv">
          <dt>Customer</dt>
          <dd>{{ customer(o) }}</dd>
          <dt>Restaurant</dt>
          <dd>{{ o.restaurant || '—' }}</dd>
          <dt>Notes</dt>
          <dd>{{ o.notes || '—' }}</dd>
        </dl>
        <dl class="kv">
          <dt>Source</dt>
          <dd>{{ label(SOURCE_LABEL, o.source) }} · {{ o.channel }}</dd>
          <dt>External id</dt>
          <dd class="mono">{{ o.external_id }}</dd>
          <dt>Total</dt>
          <dd class="num">{{ fmtMoney(o.total) }}</dd>
        </dl>
        <dl class="kv">
          <dt>Type</dt>
          <dd v-if="o.kind === 'scheduled'">Scheduled · {{ o.meal }} {{ fmtTime(o.scheduled_for) }}</dd>
          <dd v-else>Real-time</dd>
          <dt>Received</dt>
          <dd>{{ fmtTime(o.created_at, true) }}</dd>
          <dt>Dispatched</dt>
          <dd>{{ fmtTime(o.dispatched_at, true) }}</dd>
        </dl>
      </div>
    </section>

    <div class="grid">
      <div class="col">
        <section class="card">
          <div class="card-head"><h2>Items</h2><span class="muted">{{ detail.items.length }} lines</span></div>
          <table class="items">
            <thead>
              <tr>
                <th>Item</th>
                <th>Category</th>
                <th v-if="o.source === 'api'">Upstream</th>
                <th class="r">Qty</th>
                <th v-if="hasPrices" class="r">Price</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="it in detail.items" :key="it.key">
                <td>{{ it.name }}</td>
                <td class="muted">{{ it.category || '—' }}</td>
                <td v-if="o.source === 'api'"><span class="chip chip-neutral">{{ it.external_status }}</span></td>
                <td class="r num">{{ it.qty }}</td>
                <td v-if="hasPrices" class="r num">{{ fmtMoney(it.price) }}</td>
              </tr>
              <tr v-if="!detail.items.length"><td colspan="5" class="empty">No items</td></tr>
            </tbody>
            <tfoot v-if="hasPrices">
              <tr>
                <td :colspan="o.source === 'api' ? 4 : 3" class="r muted">Item total</td>
                <td class="r num"><b>{{ fmtMoney(itemsTotal) }}</b></td>
              </tr>
            </tfoot>
          </table>
          <p v-if="!hasPrices && o.source !== 'csv'" class="card-body muted note">This source sends an order total but no per-item prices.</p>
        </section>

        <section class="card">
          <div class="card-head"><h2>History</h2><span class="muted">{{ detail.events.length }} events · newest first</span></div>
          <div class="card-body"><Timeline :events="events" /></div>
        </section>
      </div>

      <div class="col">
        <section class="card">
          <div class="card-head">
            <h2>Robot payload</h2>
            <span class="chip" :class="{ 'chip-neutral': detail.robot.status !== 'failed' }">
              {{ detail.robot.status === 'preview' ? 'Preview (not dispatched yet)' : `${detail.robot.status} · ${detail.robot.attempts} attempt(s)` }}
            </span>
          </div>
          <div class="card-body">
            <p v-if="detail.robot.last_error" class="err">Last error: {{ detail.robot.last_error }}</p>
            <JsonViewer :value="detail.robot.payload" :collapsed-lines="40" />
          </div>
        </section>

        <section class="card">
          <div class="card-head"><h2>Related deliveries</h2></div>
          <div class="card-body">
            <h3 class="sub">ID conflicts <span class="muted">({{ related?.conflicts.length ?? 0 }})</span></h3>
            <p v-if="!related?.conflicts.length" class="muted">No other payloads have used this order id.</p>
            <div v-for="c in related?.conflicts" :key="c.id" class="rel">
              <div class="row">
                <span class="chip">quarantined</span>
                <span class="muted">{{ fmtTime(c.created_at, true) }}</span>
                <RouterLink to="/review" class="spacer-left">Review queue →</RouterLink>
              </div>
              <p class="muted small">{{ c.detail }}</p>
              <JsonViewer :value="c.payload" max-height="220px" />
            </div>

            <h3 class="sub">Duplicate deliveries dropped <span class="muted">({{ related?.duplicate_count ?? 0 }})</span></h3>
            <p v-if="!related?.duplicate_count" class="muted">None; this order was delivered once.</p>
            <ul v-else class="dups">
              <li v-for="d in related?.duplicates" :key="d.inbox_id">
                <span class="num">{{ fmtTime(d.received_at, true) }}</span>
                <span v-if="d.upload_id" class="muted"> · CSV upload #{{ d.upload_id }}</span>
                <span class="muted"> · inbox #{{ d.inbox_id }}</span>
              </li>
            </ul>
          </div>
        </section>
      </div>
    </div>
  </template>
</template>

<style scoped>
.back {
  display: inline-block;
  margin-bottom: 10px;
  color: var(--text-2);
}
.head {
  padding: 16px;
  margin-bottom: 16px;
}
.title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.title-row h2 {
  font-size: 18px;
  margin-right: 4px;
}
.confirm {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
}
.confirm .input {
  width: 240px;
}
.facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 12px 32px;
  margin-top: 14px;
}
.grid {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}
.col {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}
@media (max-width: 1100px) {
  .grid {
    grid-template-columns: 1fr;
  }
}
.items {
  width: 100%;
  border-collapse: collapse;
}
.items th {
  padding: 7px 16px;
  text-align: left;
  color: var(--text-3);
  font-size: 11.5px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  border-bottom: 1px solid var(--border);
}
.items td {
  padding: 7px 16px;
  border-bottom: 1px solid var(--border);
}
.items tfoot td {
  border-bottom: 0;
}
.r {
  text-align: right !important;
}
.note {
  margin: 0;
  font-size: 12px;
}
.sub {
  font-size: 13px;
  margin: 4px 0 6px;
}
.sub:not(:first-child) {
  margin-top: 16px;
}
.rel {
  padding: 10px 0;
  border-top: 1px solid var(--border);
}
.small {
  font-size: 12px;
  margin: 4px 0 8px;
}
.spacer-left {
  margin-left: auto;
}
.dups {
  margin: 0;
  padding-left: 18px;
}
.err {
  color: var(--danger);
  margin: 0 0 8px;
}
</style>
