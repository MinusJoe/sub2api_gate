<template>
  <div ref="root" role="dialog" :aria-label="t('admin.users.columnSettings')" class="w-72 max-w-[calc(100vw-2rem)] rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-800" @keydown.esc.stop.prevent="emit('close')">
    <p class="border-b border-gray-100 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">{{ t('usage.columnOrder.hint') }}</p>
    <VueDraggable v-model="items" handle=".column-drag-handle" :animation="120" class="max-h-80 overflow-y-auto p-1" data-testid="column-order-list">
      <div v-for="(column, index) in items" :key="column.key" class="flex items-center gap-1 rounded-md px-1 hover:bg-gray-50 dark:hover:bg-dark-700" :data-column-key="column.key">
        <span class="column-drag-handle cursor-grab p-1 text-gray-400 active:cursor-grabbing" :title="t('usage.columnOrder.drag', { column: column.label })" aria-hidden="true">
          <Icon name="arrowsUpDown" size="sm" />
        </span>
        <button
          type="button"
          class="flex min-w-0 flex-1 items-center justify-between gap-2 py-2 text-left text-sm text-gray-700 disabled:cursor-default dark:text-gray-200"
          :data-testid="`usage-column-toggle-${column.key}`"
          :aria-pressed="visibleKeys.includes(column.key)"
          :disabled="lockedKeys.includes(column.key)"
          :title="lockedKeys.includes(column.key) ? t('usage.columnOrder.required') : undefined"
          @click="emit('toggle', column.key)"
        >
          <span class="truncate">{{ column.label }}</span>
          <Icon v-if="visibleKeys.includes(column.key)" name="check" size="sm" class="shrink-0 text-primary-500" />
        </button>
        <button type="button" class="rounded p-1 text-gray-500 hover:bg-gray-100 disabled:opacity-25 dark:hover:bg-dark-600" :aria-label="t('usage.columnOrder.moveUp', { column: column.label })" :data-testid="`column-up-${column.key}`" :disabled="index === 0" @click="move(index, -1)">
          <Icon name="arrowUp" size="sm" />
        </button>
        <button type="button" class="rounded p-1 text-gray-500 hover:bg-gray-100 disabled:opacity-25 dark:hover:bg-dark-600" :aria-label="t('usage.columnOrder.moveDown', { column: column.label })" :data-testid="`column-down-${column.key}`" :disabled="index === items.length - 1" @click="move(index, 1)">
          <Icon name="arrowDown" size="sm" />
        </button>
      </div>
    </VueDraggable>
    <div class="border-t border-gray-100 p-2 dark:border-dark-700">
      <button type="button" class="w-full rounded px-2 py-1.5 text-left text-xs text-primary-700 hover:bg-gray-50 dark:text-primary-300 dark:hover:bg-dark-700" data-testid="column-order-reset" @click="emit('reset')">{{ t('usage.columnOrder.reset') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { VueDraggable } from 'vue-draggable-plus'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  columns: Array<{ key: string; label: string }>
  visibleKeys: string[]
  lockedKeys: string[]
}>()
const emit = defineEmits<{
  toggle: [key: string]
  reorder: [keys: string[]]
  reset: []
  close: []
}>()
const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const items = computed({
  get: () => props.columns.map(column => ({ ...column })),
  set: columns => emit('reorder', columns.map(column => column.key))
})
async function move(index: number, offset: number) {
  const keys = props.columns.map(column => column.key)
  const movedKey = keys[index]
  const target = index + offset
  if (target < 0 || target >= keys.length) return
  ;[keys[index], keys[target]] = [keys[target], keys[index]]
  emit('reorder', keys)
  await nextTick()
  // Moving to an edge disables the clicked arrow. Keep keyboard focus on the same row.
  const direction = target === 0 ? 'down' : target === keys.length - 1 ? 'up' : offset < 0 ? 'up' : 'down'
  const button = Array.from(root.value?.querySelectorAll<HTMLButtonElement>('button[data-testid]') || [])
    .find(button => button.dataset.testid === `column-${direction}-${movedKey}`)
  button?.focus()
}
</script>
