<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '../api/client'
import type { DeadLetter } from '../api/types'
import JsonViewer from '../components/JsonViewer.vue'
import { bump, dataVersion } from '../composables/useActions'
import { usePolling } from '../composables/usePolling'
import { useToast } from '../composables/useToast'
import { fmtTime, shortId } from '../format'

const toast = useToast()
const items = ref<DeadLetter[] | null>(null)
const showResolved = ref(false)
const open = ref(new Set<number>())

const { refresh } = usePolling(async () => {
  items.value = await api.deadLetter(showResolved.value)
}, 5000)
watch([showResolved, dataVersion], () => refresh())

function toggle(id: number) {
  const s = new Set(open.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  open.value = s
}
async function resolve(d: DeadLetter) {
  try {
    await api.resolveDeadLetter(d.id)
    toast.push({ kind: 'success', title: `Marked #${d.id} as resolved` })
    bump()
  } catch (e) {
    toast.error(e)
  }
}

const REASON: Record<string, string> = {
  id_conflict: 'ID conflict',
  empty_order: 'Empty order',
  zero_total: 'Zero total',
  invalid_payload: 'Invalid payload',
  max_attempts_exceeded: 'Retries exhausted',
}
</script>

<template>
  <div class="card">
    <div class="card-head">
      <div>
        <h2>Dead letters &amp; conflicts</h2>
        <p class="muted intro">Payloads the system refused or could not process. Nothing here blocked other orders.</p>
      </div>
      <label class="check"><input v-model="showResolved" type="checkbox" /> Show resolved</label>
    </div>
    <div v-if="items && !items.length" class="empty">The review queue is empty.</div>
    <ul v-else class="list">
      <li v-for="d in items ?? []" :key="d.id" :class="{ resolved: d.resolved_at }">
        <div class="line">
          <span class="chip" :class="{ 'chip-neutral': d.resolved_at }">{{ REASON[d.reason] ?? d.reason }}</span>
          <span class="mono">{{ d.source }} · {{ shortId(d.external_id) || '—' }}</span>
          <RouterLink v-if="d.order_id" :to="`/orders/${d.order_id}`">Order #{{ d.order_id }}</RouterLink>
          <span class="spacer" />
          <span class="muted num">{{ fmtTime(d.created_at, true) }}</span>
          <button class="btn btn-sm" type="button" @click="toggle(d.id)">{{ open.has(d.id) ? 'Hide' : 'Payload' }}</button>
          <button v-if="!d.resolved_at" class="btn btn-sm" type="button" @click="resolve(d)">Resolve</button>
          <span v-else class="muted small">resolved {{ fmtTime(d.resolved_at) }}</span>
        </div>
        <div class="detail">{{ d.detail }}</div>
        <JsonViewer v-if="open.has(d.id)" :value="d.payload" max-height="320px" />
      </li>
    </ul>
  </div>
</template>

<style scoped>
.intro {
  margin: 2px 0 0;
  font-size: 12.5px;
}
.check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-2);
  cursor: pointer;
  white-space: nowrap;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.list li {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}
.list li:last-child {
  border-bottom: 0;
}
.resolved {
  opacity: 0.65;
}
.line {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.detail {
  margin: 4px 0 6px;
  color: var(--text-2);
}
.small {
  font-size: 12px;
}
</style>
