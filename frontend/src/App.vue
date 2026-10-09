<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api/client'
import type { OrderQuery, Stats } from './api/types'
import ToastHost from './components/ToastHost.vue'
import { useCsvUpload } from './composables/useActions'
import { usePolling } from './composables/usePolling'
import { useTheme } from './composables/useTheme'

const route = useRoute()
const { pref, cycle } = useTheme()
const { uploading, upload } = useCsvUpload()
const fileInput = ref<HTMLInputElement>()

const stats = ref<Stats | null>(null)
const online = ref(true)
usePolling(async () => {
  try {
    stats.value = await api.stats()
    online.value = true
  } catch (e) {
    online.value = false
    throw e
  }
}, 5000)

function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const f = input.files?.[0]
  if (f) upload(f)
  input.value = ''
}

// Download exports whatever the orders list is currently filtered to.
const exportHref = computed(() => {
  const q: OrderQuery = route.name === 'orders' ? { ...(route.query as Record<string, string>) } : {}
  return api.exportUrl(q)
})

const nav = [
  { to: '/orders', label: 'Orders', icon: 'M4 6h16M4 12h16M4 18h10' },
  { to: '/ingestion', label: 'Ingestion', icon: 'M12 3v12m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2' },
  { to: '/review', label: 'Review queue', icon: 'M12 9v4m0 4h.01M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z' },
]
const themeLabel = computed(() => ({ system: 'Auto', light: 'Light', dark: 'Dark' })[pref.value])
const warped = computed(() => stats.value && stats.value.clock.offset !== '0 seconds' && stats.value.clock.offset !== '00:00:00')
const clock = computed(() =>
  stats.value ? new Date(stats.value.clock.now).toLocaleString(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' }) : '',
)
</script>

<template>
  <div class="shell">
    <aside class="side">
      <div class="brand">
        <svg viewBox="0 0 32 32" width="22" height="22" aria-hidden="true">
          <rect width="32" height="32" rx="7" fill="var(--accent)" />
          <path d="M9 11h14M9 16h14M9 21h9" stroke="#fff" stroke-width="2.6" stroke-linecap="round" />
        </svg>
        <span>Order Ops</span>
      </div>
      <nav>
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-link">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="n.icon" /></svg>
          <span>{{ n.label }}</span>
          <span v-if="n.to === '/review' && stats?.dead_letter_open" class="count">{{ stats.dead_letter_open }}</span>
        </RouterLink>
      </nav>
      <div class="side-foot">
        <div class="health" :class="{ off: !online }">
          <span class="pulse" />{{ online ? 'API connected' : 'API unreachable' }}
        </div>
        <div v-if="stats" class="muted small">
          Inbox backlog <b class="num">{{ stats.inbox.pending }}</b> · {{ stats.throughput.processed_last_min }}/min
        </div>
        <div v-if="warped" class="warp small" title="Time warp is active (Ingestion → Clock)">⏩ Clock: {{ clock }}</div>
        <button class="btn btn-sm btn-ghost theme" type="button" @click="cycle">Theme: {{ themeLabel }}</button>
      </div>
    </aside>

    <div class="main">
      <header class="top">
        <h1>{{ route.meta.title }}</h1>
        <span class="spacer" />
        <a class="btn" :href="exportHref" download>
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 4v11m0 0-4-4m4 4 4-4M5 20h14" /></svg>
          Download CSV
        </a>
        <button class="btn btn-primary" type="button" :disabled="uploading" @click="fileInput?.click()">
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 20V9m0 0-4 4m4-4 4 4M5 4h14" /></svg>
          {{ uploading ? 'Uploading…' : 'Upload CSV' }}
        </button>
        <input ref="fileInput" type="file" accept=".csv,text/csv" hidden @change="onFile" />
      </header>
      <main class="content">
        <RouterView />
      </main>
    </div>
    <ToastHost />
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 216px 1fr;
  min-height: 100vh;
  /* keeps the sidebar column filled on pages taller than the viewport */
  background: linear-gradient(to right, var(--surface) 215px, var(--border) 215px 216px, transparent 216px);
}
.side {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 14px 10px;
  border-right: 1px solid var(--border);
  background: var(--surface);
}
.brand {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 4px 8px 16px;
  font-weight: 650;
  font-size: 15px;
}
nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.nav-link {
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 10px;
  height: 34px;
  padding: 0 10px;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  font-weight: 500;
}
.nav-link:hover {
  background: var(--surface-2);
  color: var(--text);
  text-decoration: none;
}
.nav-link.router-link-active {
  background: var(--accent-soft);
  color: var(--accent);
}
.count {
  margin-left: auto;
  min-width: 20px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 11.5px;
  font-weight: 600;
  text-align: center;
}
.side-foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
}
.small {
  font-size: 12px;
}
.health {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
  color: var(--text-2);
}
.pulse {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--st-completed);
  box-shadow: 0 0 0 3px var(--st-completed-bg);
}
.health.off .pulse {
  background: var(--danger);
  box-shadow: 0 0 0 3px var(--danger-soft);
}
.warp {
  color: var(--st-scheduled);
  font-weight: 500;
}
.theme {
  align-self: flex-start;
  margin-left: -9px;
  color: var(--text-3);
}
.main {
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.top {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 56px;
  padding: 0 24px;
  border-bottom: 1px solid var(--border);
  background: color-mix(in srgb, var(--bg) 88%, transparent);
  backdrop-filter: blur(6px);
}
.top h1 {
  font-size: 17px;
}
.top a.btn:hover {
  text-decoration: none;
}
.content {
  padding: 20px 24px 40px;
  max-width: 1480px;
  width: 100%;
}
@media (max-width: 820px) {
  .shell {
    grid-template-columns: 1fr;
  }
  .side {
    position: static;
    height: auto;
    flex-direction: row;
    align-items: center;
    padding: 8px 12px;
    border-right: 0;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
  }
  .shell {
    background: none;
  }
  .brand {
    padding: 0 10px 0 0;
  }
  .brand span {
    display: none;
  }
  .top {
    height: auto;
    min-height: 56px;
    flex-wrap: wrap;
    padding-top: 8px;
    padding-bottom: 8px;
  }
  nav {
    flex-direction: row;
  }
  .side-foot {
    display: none;
  }
  .top,
  .content {
    padding-left: 16px;
    padding-right: 16px;
  }
}
</style>
