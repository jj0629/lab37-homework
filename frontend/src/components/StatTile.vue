<script setup lang="ts">
defineProps<{ label: string; value: number | string; tone?: string; hint?: string; active?: boolean; clickable?: boolean }>()
defineEmits<{ click: [] }>()
</script>

<template>
  <component
    :is="clickable ? 'button' : 'div'"
    class="tile"
    :class="{ active, clickable }"
    :style="tone ? { '--tone': `var(--st-${tone})` } : undefined"
    @click="clickable && $emit('click')"
  >
    <div class="label">{{ label }}</div>
    <div class="value num">{{ value }}</div>
    <div v-if="hint" class="hint">{{ hint }}</div>
  </component>
</template>

<style scoped>
.tile {
  --tone: var(--text-3);
  position: relative;
  display: block;
  min-width: 0;
  padding: 11px 14px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow);
  text-align: left;
  font: inherit;
  color: inherit;
  overflow: hidden;
}
.tile::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--tone);
  opacity: 0.85;
}
.clickable {
  cursor: pointer;
  transition: border-color 0.12s, background 0.12s;
}
.clickable:hover {
  border-color: var(--border-strong);
}
.clickable:focus-visible {
  outline: none;
  box-shadow: var(--focus);
}
.active {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.label {
  color: var(--text-3);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.value {
  margin-top: 2px;
  font-size: 22px;
  font-weight: 600;
  letter-spacing: -0.02em;
}
.hint {
  color: var(--text-3);
  font-size: 11.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
