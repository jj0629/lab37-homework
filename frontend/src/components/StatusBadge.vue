<script setup lang="ts">
import { computed } from 'vue'
import { STATUS_LABEL, label } from '../format'

const props = defineProps<{ status: string; small?: boolean }>()
const style = computed(() => ({
  color: `var(--st-${props.status}, var(--text-2))`,
  background: `var(--st-${props.status}-bg, var(--surface-2))`,
}))
</script>

<template>
  <span class="badge" :class="{ small, strike: status === 'cancelled' }" :style="style">
    <span class="dot" />{{ label(STATUS_LABEL, status) }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 9px 0 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 550;
  white-space: nowrap;
}
.badge.small {
  height: 19px;
  padding: 0 7px 0 6px;
  font-size: 11.5px;
}
.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.strike {
  text-decoration: line-through;
  text-decoration-thickness: 1px;
}
</style>
