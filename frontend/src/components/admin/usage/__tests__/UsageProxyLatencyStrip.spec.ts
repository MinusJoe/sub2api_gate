import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UsageProxyLatencyStrip from '../UsageProxyLatencyStrip.vue'
import { saveProxyAutoTestTargets } from '@/utils/proxyAutoTestTargets'

const list = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/proxies', () => ({ proxiesAPI: { list } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }) }))
const data = [1, 2, 3, 4, 5].map(id => ({ id, name: `proxy-${id}`, latency_ms: id === 1 ? 0 : id * 50, latency_status: 'success', latency_checked_at: 1793180000, password: 'do-not-render-password', host: 'do-not-render-host' }))
let wrapper: ReturnType<typeof mount>
const visibleIDs = () => wrapper.findAll('[data-proxy-id]').map(item => Number(item.attributes('data-proxy-id')))
async function setup(ids: number[]) {
  saveProxyAutoTestTargets(ids)
  wrapper = mount(UsageProxyLatencyStrip, { global: { stubs: { Icon: true } } })
  await flushPromises()
}
beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  list.mockReset().mockResolvedValue({ items: data, pages: 1 })
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: false }))
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('selected auto-test proxy summary', () => {
  it('shows only the configured auto-test IDs, including a real zero latency', async () => {
    await setup([3, 1])
    expect(visibleIDs()).toEqual([3, 1])
    expect(wrapper.text()).toContain('0 ms')
    expect(wrapper.text()).not.toContain('proxy-2')
    expect(wrapper.text()).not.toContain('do-not-render')
    expect(wrapper.find('[data-testid="proxy-latency-page"]').exists()).toBe(false)
  })
  it('does not fall back to all proxies when no automatic targets were selected', async () => {
    await setup([])
    await vi.advanceTimersByTimeAsync(20000)
    expect(list).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('usage.proxyLatency.empty')
  })
  it('rotates in pairs every five seconds and wraps an odd final page', async () => {
    await setup([1, 2, 3, 4, 5])
    expect(visibleIDs()).toEqual([1, 2])
    await vi.advanceTimersByTimeAsync(5000)
    expect(visibleIDs()).toEqual([3, 4])
    await vi.advanceTimersByTimeAsync(5000)
    expect(visibleIDs()).toEqual([5])
    await vi.advanceTimersByTimeAsync(5000)
    expect(visibleIDs()).toEqual([1, 2])
  })
  it('pauses on hover and supports manual next and pause', async () => {
    await setup([1, 2, 3])
    await wrapper.get('section').trigger('mouseenter')
    await vi.advanceTimersByTimeAsync(5000)
    expect(visibleIDs()).toEqual([1, 2])
    await wrapper.get('button[aria-label="usage.proxyLatency.next"]').trigger('click')
    expect(visibleIDs()).toEqual([3])
    await wrapper.get('section').trigger('mouseleave')
    await wrapper.get('button[aria-pressed]').trigger('click')
    await vi.advanceTimersByTimeAsync(5000)
    expect(visibleIDs()).toEqual([3])
  })
  it('looks across pages for selected IDs and distinguishes failed and untested results', async () => {
    list.mockImplementation(async (page: number) => page === 1
      ? { items: data.slice(0, 3), pages: 2 }
      : { items: [{ id: 4, name: 'proxy-4', latency_status: 'failed' }, { id: 5, name: 'proxy-5' }], pages: 2 })
    await setup([4, 5])
    expect(visibleIDs()).toEqual([4, 5])
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('usage.proxyLatency.failed')
    expect(wrapper.text()).toContain('usage.proxyLatency.untested')
    expect(wrapper.text()).toContain('usage.proxyLatency.noSampleTime')
  })
  it('labels retained results stale after a fetch error and cancels a pending request on unmount', async () => {
    await setup([1])
    list.mockRejectedValueOnce(new Error('offline'))
    await vi.advanceTimersByTimeAsync(10000)
    expect(visibleIDs()).toEqual([1])
    expect(wrapper.text()).toContain('usage.proxyLatency.stale')
    list.mockImplementation(() => new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(10000)
    const signal = list.mock.lastCall![3].signal as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })
})
