import { computed, ref, type Ref } from 'vue'

export function useColumnOrder<T extends { key: string }>(columns: Ref<T[]>, storageKey: string) {
  const savedOrder = ref<string[]>([])
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(storageKey) || '[]')
    if (Array.isArray(saved)) savedOrder.value = saved.filter((key): key is string => typeof key === 'string')
  } catch { /* Invalid or unavailable storage falls back to the default order. */ }

  const orderedColumns = computed(() => {
    const byKey = new Map(columns.value.map(column => [column.key, column]))
    const keys = [...new Set([...savedOrder.value, ...byKey.keys()])]
    return keys.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
  })

  function setOrder(keys: string[]) {
    const validKeys = new Set(columns.value.map(column => column.key))
    savedOrder.value = [...new Set(keys.filter(key => validKeys.has(key)))]
    try { localStorage.setItem(storageKey, JSON.stringify(orderedColumns.value.map(column => column.key))) } catch { /* optional */ }
  }
  function resetOrder() {
    savedOrder.value = []
    try { localStorage.removeItem(storageKey) } catch { /* optional */ }
  }
  return { orderedColumns, setOrder, resetOrder }
}
