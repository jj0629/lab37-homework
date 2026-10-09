<script setup lang="ts">
import { ref } from 'vue'
import type { OrderEvent } from '../api/types'
import { fmtTime } from '../format'
import JsonViewer from './JsonViewer.vue'
import StatusBadge from './StatusBadge.vue'

defineProps<{ events: OrderEvent[] }>()
const open = ref(new Set<number>())
function toggle(id: number) {
  const s = new Set(open.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  open.value = s
}

const TONE: Record<string, string> = {
  received: 'received',
  scheduled: 'scheduled',
  released: 'queued',
  queued: 'queued',
  manual_dispatch: 'queued',
  dispatched: 'dispatched',
  robot_acknowledged: 'dispatched',
  status_changed: 'out_for_delivery',
  cancelled: 'cancelled',
  cancel_requested: 'rejected',
  rejected: 'rejected',
  conflict_quarantined: 'rejected',
  robot_delivery_failed: 'rejected',
  duplicate_received: 'received',
}
function tone(e: OrderEvent) {
  if (e.type === 'status_changed' && e.to_status) return e.to_status
  return TONE[e.type] ?? 'received'
}
function title(t: string) {
  return t.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase())
}
</script>

<template>
  <ol class="timeline">
    <li v-for="e in events" :key="e.id" :style="{ '--tone': `var(--st-${tone(e)})` }">
      <span class="dot" />
      <div class="head">
        <span class="type">{{ title(e.type) }}</span>
        <span v-if="e.to_status && e.to_status !== e.from_status" class="trans">
          <StatusBadge v-if="e.from_status" :status="e.from_status" small />
          <span v-if="e.from_status" class="muted">→</span>
          <StatusBadge :status="e.to_status" small />
        </span>
        <span class="spacer" />
        <time class="muted num" :datetime="e.created_at">{{ fmtTime(e.created_at, true) }}</time>
      </div>
      <div v-if="e.message" class="msg">{{ e.message }}</div>
      <button v-if="e.payload" class="btn btn-sm btn-ghost toggle" type="button" @click="toggle(e.id)">
        {{ open.has(e.id) ? 'Hide payload' : 'Show payload' }}
      </button>
      <JsonViewer v-if="e.payload && open.has(e.id)" :value="e.payload" max-height="280px" />
    </li>
  </ol>
</template>

<style scoped>
.timeline {
  list-style: none;
  margin: 0;
  padding: 0;
}
li {
  --tone: var(--text-3);
  position: relative;
  padding: 0 0 14px 22px;
}
li::before {
  content: '';
  position: absolute;
  left: 5px;
  top: 14px;
  bottom: -2px;
  width: 1px;
  background: var(--border-strong);
}
li:last-child::before {
  display: none;
}
.dot {
  position: absolute;
  left: 0;
  top: 4px;
  width: 11px;
  height: 11px;
  border-radius: 50%;
  background: var(--surface);
  border: 2.5px solid var(--tone);
}
.head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.type {
  font-weight: 600;
}
.trans {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.msg {
  margin-top: 2px;
  color: var(--text-2);
}
.toggle {
  margin: 2px 0 4px -9px;
  color: var(--accent);
}
</style>
