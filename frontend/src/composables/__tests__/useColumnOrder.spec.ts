import { beforeEach, describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useColumnOrder } from '../useColumnOrder'

const columns = () => ref(['time', 'model', 'cost'].map(key => ({ key, label: key })))
beforeEach(() => localStorage.clear())
describe('persistent column order', () => {
  it('persists an order and restores it without affecting hidden-column preferences', () => {
    localStorage.setItem('hidden', '["cost"]')
    const state = useColumnOrder(columns(), 'order')
    state.setOrder(['cost', 'time', 'model'])
    expect(useColumnOrder(columns(), 'order').orderedColumns.value.map(c => c.key)).toEqual(['cost', 'time', 'model'])
    state.resetOrder()
    expect(state.orderedColumns.value.map(c => c.key)).toEqual(['time', 'model', 'cost'])
    expect(localStorage.getItem('hidden')).toBe('["cost"]')
  })
  it('ignores removed and duplicated keys and appends new columns', () => {
    localStorage.setItem('order', '["cost","deleted","cost",null]')
    const source = columns()
    const state = useColumnOrder(source, 'order')
    expect(state.orderedColumns.value.map(c => c.key)).toEqual(['cost', 'time', 'model'])
    source.value.push({ key: 'latency', label: 'Latency' })
    expect(state.orderedColumns.value.map(c => c.key)).toEqual(['cost', 'time', 'model', 'latency'])
  })
  it('keeps page and tab settings separate and tolerates corrupt storage', () => {
    localStorage.setItem('user-order', '{broken')
    useColumnOrder(columns(), 'admin-order').setOrder(['model', 'cost', 'time'])
    expect(useColumnOrder(columns(), 'user-order').orderedColumns.value[0].key).toBe('time')
    expect(useColumnOrder(columns(), 'error-order').orderedColumns.value[0].key).toBe('time')
  })
})
