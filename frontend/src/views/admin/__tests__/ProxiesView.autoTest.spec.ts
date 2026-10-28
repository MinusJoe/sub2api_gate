import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'
import ProxyAutoTestControl from '@/components/admin/proxy/ProxyAutoTestControl.vue'
import { readProxyAutoTestTargets } from '@/utils/proxyAutoTestTargets'

const { list, testProxy, showSuccess, showError } = vi.hoisted(() => ({
  list: vi.fn(), testProxy: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list, testProxy, getAllWithCount: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError, showInfo: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const proxies = Array.from({ length: 7 }, (_, i) => ({ id: i + 1, name: `proxy-${i + 1}`, protocol: 'http', host: 'localhost', port: 8080 }))
let wrapper: ReturnType<typeof shallowMount>
function setup() {
  wrapper = shallowMount(ProxiesView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
    DataTable: { props: ['data'], template: `<div><div v-for="row in data" :key="row.id"><slot name="cell-select" :row="row" /><span :data-row="row.id">{{ row.latency_ms ?? '-' }}</span></div></div>` }
  } } })
  return wrapper
}
async function enable() {
  await flushPromises()
  const control = wrapper.findComponent(ProxyAutoTestControl)
  control.vm.$emit('update:interval', 1)
  control.vm.$emit('update:enabled', true)
  await vi.advanceTimersByTimeAsync(1000)
  await flushPromises()
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  localStorage.clear()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  list.mockImplementation(async () => ({ items: proxies.map(proxy => ({ ...proxy })), total: 7, pages: 1 }))
  testProxy.mockResolvedValue({ success: true, latency_ms: 42 })
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks(); vi.useRealTimers() })

describe('automatic proxy speed testing', () => {
  it('tests all matching IPs and updates latency without reloading the table or showing toasts', async () => {
    setup()
    await enable()
    expect(testProxy.mock.calls.map(([id]) => id)).toEqual([1, 2, 3, 4, 5, 6, 7])
    expect(list).toHaveBeenCalledTimes(3) // initial page + remembered scope + test round
    expect(wrapper.get('[data-row="1"]').text()).toBe('42')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
  })

  it('tests only the selected IPs', async () => {
    setup()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await enable()
    expect(testProxy).toHaveBeenCalledOnce()
    expect(testProxy.mock.calls[0][0]).toBe(1)
    expect(list).toHaveBeenCalledOnce()
    expect(readProxyAutoTestTargets()).toEqual([1])
  })

  it('remembers the selected scope before the first scheduled test, for the usage page', async () => {
    setup()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    wrapper.findComponent(ProxyAutoTestControl).vm.$emit('update:interval', 60)
    wrapper.findComponent(ProxyAutoTestControl).vm.$emit('update:enabled', true)
    await flushPromises()
    expect(readProxyAutoTestTargets()).toEqual([1])
    expect(testProxy).not.toHaveBeenCalled()
    wrapper.unmount()
    expect(readProxyAutoTestTargets()).toEqual([1])
  })

  it('includes every page of matching proxies, not only the visible page', async () => {
    list.mockImplementation(async (page, size) => size === 200
      ? { items: proxies.slice(page === 1 ? 0 : 4, page === 1 ? 4 : 7).map(proxy => ({ ...proxy })), total: 7, pages: 2 }
      : { items: proxies.slice(0, 2).map(proxy => ({ ...proxy })), total: 7, pages: 4 })
    setup()
    await enable()
    expect(testProxy.mock.calls.map(([id]) => id)).toEqual([1, 2, 3, 4, 5, 6, 7])
    expect(list.mock.calls.slice(-2).map(([page]) => page)).toEqual([1, 2])
  })

  it('limits concurrency to five and stops queued requests and late results when disabled', async () => {
    const finish: Array<() => void> = []
    testProxy.mockImplementation(() => new Promise(resolve => { finish.push(() => resolve({ success: true, latency_ms: 99 })) }))
    setup()
    await enable()
    expect(testProxy).toHaveBeenCalledTimes(5)
    await vi.advanceTimersByTimeAsync(5000)
    expect(testProxy).toHaveBeenCalledTimes(5)
    wrapper.findComponent(ProxyAutoTestControl).vm.$emit('update:enabled', false)
    expect(testProxy.mock.calls.every(([, options]) => options.signal.aborted)).toBe(true)
    finish.forEach(resolve => resolve())
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    expect(testProxy).toHaveBeenCalledTimes(5)
    expect(wrapper.get('[data-row="1"]').text()).toBe('-')
  })

  it('does not test queued IPs after navigation unmounts the page', async () => {
    const finish: Array<() => void> = []
    testProxy.mockImplementation(() => new Promise(resolve => { finish.push(() => resolve({ success: true })) }))
    setup()
    await enable()
    wrapper.unmount()
    expect(testProxy.mock.calls.every(([, options]) => options.signal.aborted)).toBe(true)
    finish.forEach(resolve => resolve())
    await flushPromises()
    expect(testProxy).toHaveBeenCalledTimes(5)
  })
})
