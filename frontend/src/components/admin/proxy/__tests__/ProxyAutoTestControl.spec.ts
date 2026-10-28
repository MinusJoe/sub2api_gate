import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ProxyAutoTestControl from '../ProxyAutoTestControl.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function setup() {
  return mount(ProxyAutoTestControl, {
    props: { enabled: false, intervalSeconds: 30, countdown: 30, running: false, paused: false, failed: false, selectedCount: 0 },
    global: { stubs: {
      Icon: true,
      Select: { props: ['modelValue', 'options'], emits: ['update:modelValue'], template: `<select :value="modelValue" @change="$emit('update:modelValue', $event.target.value === 'custom' ? 'custom' : Number($event.target.value))"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>` }
    } }
  })
}

describe('proxy test interval control', () => {
  it('offers all requested presets and a custom interval', async () => {
    const wrapper = setup()
    expect(wrapper.findAll('option').map(option => option.attributes('value'))).toEqual(['1', '3', '5', '10', '30', '60', 'custom'])
    await wrapper.get('select').setValue('1')
    expect(wrapper.emitted('update:interval')).toEqual([[1]])
    await wrapper.get('[data-testid="proxy-auto-test-toggle"]').setValue(true)
    expect(wrapper.emitted('update:enabled')).toEqual([[true]])
    wrapper.unmount()
  })

  it('keeps the interval unchanged until a valid custom value is applied', async () => {
    const wrapper = setup()
    await wrapper.get('select').setValue('custom')
    await wrapper.get('input[type="number"]').setValue(0)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.emitted('update:interval')).toBeUndefined()
    await wrapper.get('input[type="number"]').setValue(7)
    expect(wrapper.emitted('update:interval')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('update:interval')).toEqual([[7]])
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
