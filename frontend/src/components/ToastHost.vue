<script setup lang="ts">
import { ref } from 'vue'
import { useToast } from '../composables/useToast'

const { state, dismiss } = useToast()
const expanded = ref(new Set<number>())
function toggle(id: number) {
  const s = new Set(expanded.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  expanded.value = s
}
</script>

<template>
  <div class="toasts" aria-live="polite">
    <TransitionGroup name="toast">
      <div v-for="t in state.toasts" :key="t.id" class="toast" :class="t.kind" role="status">
        <div class="bar" />
        <div class="content">
          <div class="title">{{ t.title }}</div>
          <div v-if="t.body" class="body">{{ t.body }}</div>
          <template v-if="t.details?.length">
            <button class="btn btn-sm btn-ghost link" type="button" @click="toggle(t.id)">
              {{ expanded.has(t.id) ? 'Hide' : 'Show' }} details ({{ t.details.length }})
            </button>
            <ul v-if="expanded.has(t.id)" class="details">
              <li v-for="(d, i) in t.details" :key="i">{{ d }}</li>
            </ul>
          </template>
        </div>
        <button class="close btn btn-sm btn-ghost" type="button" aria-label="Dismiss" @click="dismiss(t.id)">✕</button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toasts {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 50;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: min(380px, calc(100vw - 32px));
}
.toast {
  display: flex;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow-lg);
}
.bar {
  width: 4px;
  flex: none;
  background: var(--accent);
}
.success .bar {
  background: var(--st-completed);
}
.error .bar {
  background: var(--danger);
}
.content {
  flex: 1;
  min-width: 0;
  padding: 10px 4px 10px 12px;
}
.title {
  font-weight: 600;
}
.body {
  color: var(--text-2);
}
.link {
  margin-left: -9px;
  color: var(--accent);
}
.details {
  margin: 4px 0 0;
  padding-left: 16px;
  max-height: 180px;
  overflow: auto;
  color: var(--text-2);
  font-size: 12px;
}
.close {
  align-self: flex-start;
  margin: 4px;
  color: var(--text-3);
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
.toast-enter-active,
.toast-leave-active {
  transition: all 0.18s ease;
}
</style>
