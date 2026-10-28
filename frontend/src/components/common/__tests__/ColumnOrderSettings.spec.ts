import { describe, expect, it, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import { VueDraggable } from 'vue-draggable-plus'
import ColumnOrderSettings from '../ColumnOrderSettings.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const columns = [{ key: 'time', label: 'Time' }, { key: 'model', label: 'Model' }, { key: 'cost', label: 'Cost' }]
function setup() {
  return shallowMount(ColumnOrderSettings, { props: { columns, visibleKeys: ['time', 'model'], lockedKeys: ['time'] }, global: { stubs: {
    VueDraggable: { props: ['modelValue'], emits: ['update:modelValue'], template: '<div><slot /></div>' }
  } } })
}
describe('column position controls', () => {
  it('moves required and hidden columns without changing visibility', async () => {
    const wrapper = setup()
    expect(wrapper.get('[data-testid="usage-column-toggle-time"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="column-down-time"]').trigger('click')
    expect(wrapper.emitted('reorder')).toEqual([[['model', 'time', 'cost']]])
    expect(wrapper.emitted('toggle')).toBeUndefined()
    await wrapper.get('[data-testid="usage-column-toggle-cost"]').trigger('click')
    expect(wrapper.emitted('toggle')).toEqual([['cost']])
    wrapper.unmount()
  })
  it('accepts drag order, exposes reset, and closes on Escape', async () => {
    const wrapper = setup()
    wrapper.findComponent(VueDraggable).vm.$emit('update:modelValue', [columns[2], columns[0], columns[1]])
    expect(wrapper.emitted('reorder')).toEqual([[['cost', 'time', 'model']]])
    await wrapper.get('[data-testid="column-order-reset"]').trigger('click')
    expect(wrapper.emitted('reset')).toHaveLength(1)
    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
