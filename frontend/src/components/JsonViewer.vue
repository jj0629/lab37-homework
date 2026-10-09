<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{ value: unknown; maxHeight?: string; collapsedLines?: number }>()
const expanded = ref(false)

const text = computed(() => {
  try {
    return JSON.stringify(props.value, null, 2) ?? 'null'
  } catch {
    return String(props.value)
  }
})
const lineCount = computed(() => text.value.split('\n').length)
const limit = computed(() => props.collapsedLines ?? 0)
const shown = computed(() =>
  limit.value && !expanded.value && lineCount.value > limit.value
    ? text.value.split('\n').slice(0, limit.value).join('\n') + '\n…'
    : text.value,
)

function esc(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
// Tiny highlighter: keys, strings, numbers, literals.
const html = computed(() =>
  esc(shown.value).replace(
    /("(?:\\.|[^"\\])*")(\s*:)?|\b(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)\b|\b(true|false|null)\b/g,
    (m, str, colon, num, lit) => {
      if (str) return colon ? `<span class="k">${str}</span>${colon}` : `<span class="s">${str}</span>`
      if (num) return `<span class="n">${num}</span>`
      if (lit) return `<span class="l">${lit}</span>`
      return m
    },
  ),
)
const copied = ref(false)
async function copy() {
  try {
    await navigator.clipboard.writeText(text.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 1200)
  } catch {
    /* clipboard unavailable */
  }
}
</script>

<template>
  <div class="json">
    <button class="copy btn btn-sm btn-ghost" type="button" @click="copy">{{ copied ? 'Copied' : 'Copy' }}</button>
    <pre :style="{ maxHeight: expanded ? 'none' : (maxHeight ?? '360px') }"><code v-html="html" /></pre>
    <button
      v-if="limit && lineCount > limit"
      class="more btn btn-sm btn-ghost"
      type="button"
      @click="expanded = !expanded"
    >
      {{ expanded ? 'Collapse' : `Show all ${lineCount} lines` }}
    </button>
  </div>
</template>

<style scoped>
.json {
  position: relative;
}
pre {
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  font: 12px/1.5 var(--mono);
  white-space: pre;
}
.copy {
  position: absolute;
  top: 4px;
  right: 6px;
  opacity: 0.7;
}
.copy:hover {
  opacity: 1;
}
.more {
  margin-top: 4px;
}
:deep(.k) {
  color: var(--st-dispatched);
}
:deep(.s) {
  color: var(--st-completed);
}
:deep(.n) {
  color: var(--st-queued);
}
:deep(.l) {
  color: var(--st-scheduled);
}
</style>
