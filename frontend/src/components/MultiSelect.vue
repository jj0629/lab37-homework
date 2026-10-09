<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { summarizeSelection } from '../filters'

export interface MultiSelectOption {
  value: string
  label: string
  /** optional count shown muted on the right (e.g. orders per status) */
  count?: number
}

const props = defineProps<{
  label: string
  options: MultiSelectOption[]
  modelValue: string[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()

const id = `ms-${Math.random().toString(36).slice(2, 10)}`
const open = ref(false)
const active = ref(0)
const alignRight = ref(false)
const root = ref<HTMLElement>()
const button = ref<HTMLButtonElement>()
const list = ref<HTMLElement>()
const pop = ref<HTMLElement>()

const selected = computed(() => new Set(props.modelValue))
const summary = computed(() =>
  summarizeSelection(
    props.label,
    props.options.filter((o) => selected.value.has(o.value)).map((o) => o.label),
  ),
)
const fmt = new Intl.NumberFormat('en-US')

function toggle(value: string) {
  const next = selected.value.has(value) ? props.modelValue.filter((v) => v !== value) : [...props.modelValue, value]
  // keep the option order stable regardless of click order
  emit('update:modelValue', props.options.map((o) => o.value).filter((v) => next.includes(v)))
}
function clear() {
  emit('update:modelValue', [])
  list.value?.focus()
}

async function show() {
  open.value = true
  const first = props.options.findIndex((o) => selected.value.has(o.value))
  active.value = first < 0 ? 0 : first
  alignRight.value = false
  await nextTick()
  const r = pop.value?.getBoundingClientRect()
  if (r && r.right > window.innerWidth - 8) alignRight.value = true
  list.value?.focus()
}
function hide(returnFocus = true) {
  if (!open.value) return
  open.value = false
  if (returnFocus) button.value?.focus()
}

function onButtonKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    show()
  }
}
function onListKey(e: KeyboardEvent) {
  const n = props.options.length
  switch (e.key) {
    case 'ArrowDown':
      active.value = (active.value + 1) % n
      break
    case 'ArrowUp':
      active.value = (active.value - 1 + n) % n
      break
    case 'Home':
      active.value = 0
      break
    case 'End':
      active.value = n - 1
      break
    case ' ':
    case 'Enter':
      if (props.options[active.value]) toggle(props.options[active.value].value)
      break
    case 'Escape':
      hide()
      break
    case 'Tab':
      hide(false)
      return
    default:
      return
  }
  e.preventDefault()
  e.stopPropagation()
}
watch(active, async (i) => {
  await nextTick()
  document.getElementById(`${id}-opt-${i}`)?.scrollIntoView({ block: 'nearest' })
})

function onDocPointer(e: PointerEvent) {
  if (root.value && !root.value.contains(e.target as Node)) hide(false)
}
watch(open, (v) => {
  if (v) document.addEventListener('pointerdown', onDocPointer, true)
  else document.removeEventListener('pointerdown', onDocPointer, true)
})
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocPointer, true))
</script>

<template>
  <div ref="root" class="ms" @keydown.esc="hide()">
    <button
      ref="button"
      type="button"
      class="ms-btn"
      :class="{ on: modelValue.length > 0 }"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="`${id}-list`"
      :title="summary"
      @click="open ? hide() : show()"
      @keydown="onButtonKey"
    >
      <span class="ms-text">{{ summary }}</span>
      <svg class="ms-caret" viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
        <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>

    <div v-if="open" ref="pop" class="ms-pop" :class="{ right: alignRight }">
      <div class="ms-head">
        <span class="ms-title">{{ label }}</span>
        <button type="button" class="ms-clear" :disabled="modelValue.length === 0" @click="clear">Clear</button>
      </div>
      <ul
        :id="`${id}-list`"
        ref="list"
        class="ms-list"
        role="listbox"
        aria-multiselectable="true"
        :aria-label="label"
        :aria-activedescendant="`${id}-opt-${active}`"
        tabindex="0"
        @keydown="onListKey"
      >
        <li
          v-for="(o, i) in options"
          :id="`${id}-opt-${i}`"
          :key="o.value"
          role="option"
          class="ms-opt"
          :class="{ active: i === active }"
          :aria-selected="selected.has(o.value)"
          @click="toggle(o.value)"
          @pointermove="active = i"
        >
          <span class="ms-box" aria-hidden="true">
            <svg v-if="selected.has(o.value)" viewBox="0 0 16 16" width="12" height="12">
              <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </span>
          <span class="ms-label">{{ o.label }}</span>
          <span v-if="o.count !== undefined" class="ms-count num">{{ fmt.format(o.count) }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.ms {
  position: relative;
  min-width: 0;
}
.ms-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  max-width: 240px;
  padding: 0 8px 0 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  white-space: nowrap;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s;
}
.ms-btn:hover {
  background: var(--surface-2);
}
.ms-btn:focus-visible {
  outline: none;
  box-shadow: var(--focus);
}
.ms-btn.on {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 500;
}
.ms-text {
  overflow: hidden;
  text-overflow: ellipsis;
}
.ms-caret {
  flex: none;
  color: var(--text-3);
}
.ms-btn.on .ms-caret {
  color: currentColor;
}
.ms-btn[aria-expanded='true'] .ms-caret {
  transform: rotate(180deg);
}

.ms-pop {
  position: absolute;
  z-index: 30;
  top: calc(100% + 4px);
  left: 0;
  min-width: 220px;
  max-width: calc(100vw - 32px);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow-lg);
  overflow: hidden;
}
.ms-pop.right {
  left: auto;
  right: 0;
}
.ms-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px 6px 12px;
  border-bottom: 1px solid var(--border);
}
.ms-title {
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-3);
}
.ms-clear {
  padding: 2px 4px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--accent);
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
}
.ms-clear:hover:not(:disabled) {
  text-decoration: underline;
}
.ms-clear:focus-visible {
  outline: none;
  box-shadow: var(--focus);
}
.ms-clear:disabled {
  color: var(--text-3);
  cursor: default;
  opacity: 0.6;
}
.ms-list {
  max-height: min(320px, 60vh);
  margin: 0;
  padding: 4px;
  overflow-y: auto;
  list-style: none;
}
.ms-list:focus {
  outline: none;
}
.ms-opt {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 6px 8px;
  border-radius: var(--radius-sm);
  white-space: nowrap;
  cursor: pointer;
  user-select: none;
}
.ms-opt.active {
  background: var(--surface-2);
}
.ms-list:focus-visible .ms-opt.active {
  box-shadow: inset 0 0 0 1px var(--accent);
}
.ms-box {
  display: inline-grid;
  place-items: center;
  flex: none;
  width: 16px;
  height: 16px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: var(--surface);
  color: #fff;
}
.ms-opt[aria-selected='true'] .ms-box {
  border-color: var(--accent);
  background: var(--accent);
}
.ms-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ms-count {
  margin-left: 12px;
  color: var(--text-3);
  font-size: 12px;
}
</style>
