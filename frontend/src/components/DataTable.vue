<script setup lang="ts" generic="T">
export interface Column {
  key: string
  label: string
  sortable?: boolean
  align?: 'left' | 'right' | 'center'
  width?: string
}

defineProps<{
  columns: Column[]
  rows: T[]
  rowKey: (row: T) => string | number
  sort?: string
  dir?: 'asc' | 'desc'
  clickable?: boolean
  loading?: boolean
  emptyText?: string
}>()
const emit = defineEmits<{ sort: [key: string]; rowClick: [row: T, ev: MouseEvent] }>()

defineSlots<Record<string, (props: { row: T }) => unknown>>()
</script>

<template>
  <div class="wrap">
    <table :class="{ clickable, loading }">
      <thead>
        <tr>
          <th
            v-for="c in columns"
            :key="c.key"
            :style="{ width: c.width, textAlign: c.align ?? 'left' }"
            :class="{ sortable: c.sortable, sorted: sort === c.key }"
            :aria-sort="sort === c.key ? (dir === 'asc' ? 'ascending' : 'descending') : undefined"
            @click="c.sortable && emit('sort', c.key)"
          >
            {{ c.label }}
            <span v-if="c.sortable" class="arrow">{{ sort === c.key ? (dir === 'asc' ? '▲' : '▼') : '↕' }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="rowKey(row)"
          :tabindex="clickable ? 0 : undefined"
          @click="emit('rowClick', row, $event)"
          @keydown.enter="emit('rowClick', row, $event as unknown as MouseEvent)"
        >
          <td v-for="c in columns" :key="c.key" :style="{ textAlign: c.align ?? 'left' }">
            <slot :name="c.key" :row="row">{{ (row as Record<string, unknown>)[c.key] }}</slot>
          </td>
        </tr>
        <tr v-if="!rows.length">
          <td :colspan="columns.length" class="empty">{{ loading ? 'Loading…' : (emptyText ?? 'Nothing here yet') }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.wrap {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
table.loading tbody {
  opacity: 0.6;
}
th {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: 8px 12px;
  background: var(--surface-2);
  border-bottom: 1px solid var(--border);
  color: var(--text-3);
  font-size: 11.5px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  user-select: none;
}
th.sortable {
  cursor: pointer;
}
th.sortable:hover,
th.sorted {
  color: var(--text);
}
.arrow {
  margin-left: 3px;
  font-size: 9px;
  opacity: 0.6;
}
td {
  padding: 7px 12px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
tbody tr:last-child td {
  border-bottom: 0;
}
.clickable tbody tr:not(:has(.empty)) {
  cursor: pointer;
}
.clickable tbody tr:hover td {
  background: var(--surface-2);
}
tbody tr:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
</style>
