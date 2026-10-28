import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'

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
    DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-select" :row="row" /></div></div>' }
  } } })
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  list.mockImplementation(async () => ({ items: proxies.map(proxy => ({ ...proxy })), total: 7, pages: 1 }))
  testProxy.mockResolvedValue({ success: true, latency_ms: 42 })
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('manual proxy connection tests', () => {
  it('does not start automatic probes while the IP page stays open', async () => {
    setup()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(120000)
    expect(testProxy).not.toHaveBeenCalled()
    expect(list).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="proxy-auto-test"]').exists()).toBe(false)
  })

  it('still tests only selected IPs on an explicit button click', async () => {
    setup()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('button[title="admin.proxies.testConnection"]').trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledOnce()
    expect(testProxy.mock.calls[0][0]).toBe(1)
    expect(showSuccess).toHaveBeenCalledOnce()
  })

  it('keeps manual batch concurrency bounded and cancels queued work on navigation', async () => {
    const finish: Array<() => void> = []
    testProxy.mockImplementation(() => new Promise(resolve => { finish.push(() => resolve({ success: true })) }))
    setup()
    await flushPromises()
    await wrapper.get('button[title="admin.proxies.testConnection"]').trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledTimes(5)
    wrapper.unmount()
    expect(testProxy.mock.calls.every(([, options]) => options.signal.aborted)).toBe(true)
    finish.forEach(resolve => resolve())
    await flushPromises()
    expect(testProxy).toHaveBeenCalledTimes(5)
    expect(showSuccess).not.toHaveBeenCalled()
  })
})
