<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Ingestion, Stats } from '../api/types'
import DataTable, { type Column } from '../components/DataTable.vue'
import { bump, importToast, useCsvUpload } from '../composables/useActions'
import { usePolling } from '../composables/usePolling'
import { useToast } from '../composables/useToast'
import { fmtAgo, fmtTime } from '../format'

const toast = useToast()
const { uploading, upload } = useCsvUpload()
const info = ref<Ingestion | null>(null)
const stats = ref<Stats | null>(null)
const samples = ref<{ name: string; bytes: number }[]>([])
const busy = ref<string | null>(null)

usePolling(async () => {
  const [i, s] = await Promise.all([api.ingestion(), api.stats()])
  info.value = i
  stats.value = s
}, 3000)
onMounted(async () => {
  try {
    samples.value = await api.samples()
  } catch {
    /* shown elsewhere */
  }
})

async function act(key: string, fn: () => Promise<void>) {
  busy.value = key
  try {
    await fn()
    bump()
  } catch (e) {
    toast.error(e)
  } finally {
    busy.value = null
  }
}

const injectCount = ref(25)
const inject = () =>
  act('inject', async () => {
    const r = await api.injectWebhook({ count: injectCount.value, fresh: true })
    toast.push({ kind: 'success', title: `Injected ${r.accepted} new webhook orders`, body: 'Random sample orders with fresh UUIDs, sent through the ingest path.' })
  })
const replay = () =>
  act('replay', async () => {
    const r = await api.injectWebhook({ count: 1004, fresh: false, offset: 0 })
    toast.push({
      kind: 'success',
      title: `Replayed ${r.accepted} webhook lines`,
      body: r.duplicates ? `${r.duplicates} were exact duplicates of earlier deliveries.` : 'Includes the file’s duplicates, cancels and id conflicts.',
    })
  })
const importSample = (name: string) => act('csv:' + name, async () => importToast(await api.importSample(name)))
const pollNow = () =>
  act('poll', async () => {
    await api.pollNow()
    toast.push({ kind: 'info', title: 'Poll requested', body: 'The poller leader will call the upstream API within a second.' })
  })
const warp = (body: { minutes?: number; to_next_release?: boolean; reset?: boolean }, key: string) =>
  act(key, async () => {
    const r = await api.timeWarp(body)
    toast.push({ kind: 'info', title: body.reset ? 'Clock reset to real time' : 'Clock moved', body: `System time is now ${fmtTime(r.now, true)}` })
  })
const armed = ref(false)
const resetAll = () => {
  if (!armed.value) {
    armed.value = true
    setTimeout(() => (armed.value = false), 4000)
    return
  }
  armed.value = false
  act('reset', async () => {
    await api.reset()
    toast.push({ kind: 'success', title: 'Demo data cleared' })
  })
}

function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const f = input.files?.[0]
  if (f) upload(f)
  input.value = ''
}
const fileInput = ref<HTMLInputElement>()

const uploadCols: Column[] = [
  { key: 'id', label: '#', width: '40px' },
  { key: 'filename', label: 'File' },
  { key: 'rows', label: 'Rows', align: 'right' },
  { key: 'new', label: 'New', align: 'right' },
  { key: 'duplicate', label: 'Seen', align: 'right' },
  { key: 'rejected', label: 'Rejected', align: 'right' },
  { key: 'created_at', label: 'When', align: 'right' },
]
const inboxRows = computed(() =>
  Object.entries(info.value?.inbox ?? {}).map(([source, c]) => ({
    source,
    done: c.done ?? 0,
    pending: c.pending ?? 0,
    duplicate: c.duplicate ?? 0,
    dead: c.dead ?? 0,
  })),
)
const mock = computed(() => info.value?.mock_api)
const warped = computed(() => stats.value && !['0 seconds', '00:00:00'].includes(stats.value.clock.offset))
</script>

<template>
  <div class="grid">
    <section class="card">
      <div class="card-head"><h2>Webhook stream</h2><span class="chip chip-neutral">real-time</span></div>
      <div class="card-body stack">
        <p class="muted">Orders pushed by delivery platforms. The edge writes each one to the inbox and returns 202; workers do the rest.</p>
        <div class="row">
          <input v-model.number="injectCount" class="input count" type="number" min="1" max="5000" aria-label="Number of orders" />
          <button class="btn btn-primary" :disabled="busy === 'inject'" @click="inject">Inject new orders</button>
          <button class="btn" :disabled="busy === 'replay'" @click="replay">Replay sample file</button>
        </div>
        <p class="muted small">Bursty load from the CLI:<br /><code>docker compose run --rm mock-webhook --fresh --count 2000 --rate 50 --burst 300</code></p>
      </div>
    </section>

    <section class="card">
      <div class="card-head"><h2>CSV uploads</h2><span class="chip chip-neutral">scheduled</span></div>
      <div class="card-body stack">
        <p class="muted">Survey exports, uploaded several times a day. Rows already seen are skipped, so re-uploading a cumulative file only adds the new rows.</p>
        <div class="row">
          <button v-for="s in samples" :key="s.name" class="btn" :disabled="busy === 'csv:' + s.name" @click="importSample(s.name)">{{ s.name }}</button>
          <button class="btn btn-primary" :disabled="uploading" @click="fileInput?.click()">Upload file…</button>
          <input ref="fileInput" type="file" accept=".csv,text/csv" hidden @change="onFile" />
        </div>
      </div>
      <DataTable :columns="uploadCols" :rows="info?.uploads ?? []" :row-key="(u) => u.id" empty-text="No uploads yet">
        <template #new="{ row }"><b class="num">{{ row.new }}</b></template>
        <template #rejected="{ row }"><span :class="{ bad: row.rejected }" class="num">{{ row.rejected }}</span></template>
        <template #created_at="{ row }"><span class="muted">{{ fmtAgo(row.created_at) }}</span></template>
      </DataTable>
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Polling API</h2>
        <button class="btn btn-sm" :disabled="busy === 'poll'" @click="pollNow">Poll now</button>
      </div>
      <div class="card-body">
        <dl class="kv">
          <dt>Last result</dt>
          <dd :class="{ bad: info?.poller.consecutive_errors }">{{ info?.poller.last_status || 'not polled yet' }}</dd>
          <dt v-if="info?.poller.last_error">Last error</dt>
          <dd v-if="info?.poller.last_error" class="bad">{{ info.poller.last_error }}</dd>
          <dt>Cursor</dt>
          <dd class="mono">{{ info?.poller.cursor || '(start)' }}</dd>
          <dt>Last poll</dt>
          <dd>{{ fmtAgo(info?.poller.last_polled_at) }}</dd>
          <dt>Poller leader</dt>
          <dd class="mono">{{ info?.poller.leader || '—' }}</dd>
          <dt>Mock upstream</dt>
          <dd v-if="mock?.error" class="bad">{{ mock.error }}</dd>
          <dd v-else-if="mock">{{ mock.released }}/{{ mock.lines }} responses released · {{ mock.served }} served · {{ mock.errors_served }} errors injected</dd>
        </dl>
        <p class="muted small">On a 500 the poller applies any partial data but keeps its cursor, then retries with backoff.</p>
      </div>
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Clock</h2>
        <span v-if="warped" class="chip">time warp on</span>
      </div>
      <div class="card-body stack">
        <dl class="kv">
          <dt>System time</dt>
          <dd><b>{{ stats ? fmtTime(stats.clock.now, true) : '—' }}</b></dd>
          <dt>Offset</dt>
          <dd class="mono">{{ stats?.clock.offset ?? '—' }}</dd>
          <dt>Scheduled</dt>
          <dd>{{ stats?.by_status.scheduled ?? 0 }} orders waiting for their slot</dd>
        </dl>
        <p class="muted small">Scheduled orders go to the robot 30 minutes before their meal slot. Move the clock forward to watch them release.</p>
        <div class="row">
          <button class="btn btn-primary" :disabled="!!busy" @click="warp({ to_next_release: true }, 'next')">Jump to next release</button>
          <button class="btn" :disabled="!!busy" @click="warp({ minutes: 60 }, 'h')">+1 hour</button>
          <button class="btn" :disabled="!!busy" @click="warp({ minutes: 360 }, '6h')">+6 hours</button>
          <button class="btn" :disabled="!!busy" @click="warp({ minutes: 1440 }, 'd')">+1 day</button>
          <button class="btn btn-ghost" :disabled="!!busy || !warped" @click="warp({ reset: true }, 'reset-clock')">Reset</button>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="card-head"><h2>Workers &amp; leaders</h2></div>
      <div class="card-body">
        <table class="mini">
          <thead><tr><th>Role</th><th>Holder</th><th>Since</th><th>Heartbeat</th></tr></thead>
          <tbody>
            <tr v-for="l in info?.leaders ?? []" :key="l.role">
              <td>{{ l.role }}</td>
              <td class="mono">{{ l.holder }}</td>
              <td class="muted">{{ fmtAgo(l.since) }}</td>
              <td><span class="hb" :class="{ dead: !l.alive }" />{{ fmtAgo(l.heartbeat) }}</td>
            </tr>
            <tr v-if="!info?.leaders.length"><td colspan="4" class="muted">No leaders yet</td></tr>
          </tbody>
        </table>
        <h3 class="sub">Inbox by source</h3>
        <table class="mini">
          <thead><tr><th>Source</th><th class="r">Processed</th><th class="r">Pending</th><th class="r">Duplicates</th><th class="r">Dead</th></tr></thead>
          <tbody>
            <tr v-for="r in inboxRows" :key="r.source">
              <td>{{ r.source }}</td>
              <td class="r num">{{ r.done }}</td>
              <td class="r num">{{ r.pending }}</td>
              <td class="r num">{{ r.duplicate }}</td>
              <td class="r num" :class="{ bad: r.dead }">{{ r.dead }}</td>
            </tr>
            <tr v-if="!inboxRows.length"><td colspan="5" class="muted">Inbox is empty</td></tr>
          </tbody>
        </table>
        <p v-if="mock?.robot" class="muted small">
          Mock robot: {{ mock.robot.unique_messages }} messages accepted, {{ mock.robot.requests - mock.robot.unique_messages }} repeat deliveries, {{ mock.robot.injected_failures }} injected 503s (retried).
        </p>
      </div>
    </section>

    <section class="card danger">
      <div class="card-head"><h2>Reset demo</h2></div>
      <div class="card-body stack">
        <p class="muted">Clears every order, event, upload and dead letter, resets the clock and rewinds the mock API.</p>
        <div><button class="btn btn-danger" :disabled="busy === 'reset'" @click="resetAll">{{ armed ? 'Click again to confirm' : 'Reset all data' }}</button></div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 460px), 1fr));
  gap: 16px;
  align-items: start;
}
.stack {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.stack p {
  margin: 0;
}
.small {
  font-size: 12px;
}
.small code {
  display: inline-block;
  margin-top: 3px;
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--surface-2);
  overflow-wrap: anywhere;
}
.count {
  width: 80px;
}
.bad {
  color: var(--danger);
}
.mini {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}
.mini th {
  padding: 5px 8px 5px 0;
  text-align: left;
  color: var(--text-3);
  font-weight: 600;
  font-size: 11.5px;
  border-bottom: 1px solid var(--border);
}
.mini td {
  padding: 5px 8px 5px 0;
  border-bottom: 1px solid var(--border);
}
.r {
  text-align: right !important;
}
.sub {
  margin: 16px 0 4px;
  font-size: 13px;
}
.hb {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 6px;
  border-radius: 50%;
  background: var(--st-completed);
}
.hb.dead {
  background: var(--danger);
}
</style>
