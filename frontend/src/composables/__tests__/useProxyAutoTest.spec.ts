import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { PROXY_TEST_INTERVALS, useProxyAutoTest } from '../useProxyAutoTest'

let wrapper: ReturnType<typeof mount>
let timer: ReturnType<typeof useProxyAutoTest>
let hidden = false

function setup(run = vi.fn().mockResolvedValue(undefined), shouldPause = () => false) {
  wrapper = mount(defineComponent({
    setup() { timer = useProxyAutoTest({ run, shouldPause }); return () => null }
  }))
  return run
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  hidden = false
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
})
afterEach(() => {
  wrapper?.unmount()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('page-scoped automatic proxy tests', () => {
  it.each(PROXY_TEST_INTERVALS)('supports the %is preset', async seconds => {
    const run = setup()
    timer.setIntervalSeconds(seconds)
    timer.setEnabled(true)
    await vi.advanceTimersByTimeAsync(seconds * 1000 - 1)
    expect(run).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(run).toHaveBeenCalledOnce()
  })

  it('accepts a custom interval and restores it without automatically starting', async () => {
    const run = setup()
    expect(timer.setIntervalSeconds(7)).toBe(true)
    timer.setEnabled(true)
    await vi.advanceTimersByTimeAsync(7000)
    expect(run).toHaveBeenCalledOnce()
    wrapper.unmount()
    const reopened = setup()
    expect(timer.intervalSeconds.value).toBe(7)
    expect(timer.enabled.value).toBe(false)
    await vi.advanceTimersByTimeAsync(20000)
    expect(reopened).not.toHaveBeenCalled()
  })

  it.each([0, -1, 0.5, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('rejects invalid interval %s', value => {
    setup()
    expect(timer.setIntervalSeconds(value)).toBe(false)
    expect(timer.intervalSeconds.value).toBe(30)
  })

  it('waits for a slow round to finish before counting the next interval', async () => {
    let finish!: () => void
    const run = setup(vi.fn().mockImplementation(() => new Promise<void>(resolve => { finish = resolve })))
    timer.setIntervalSeconds(1)
    timer.setEnabled(true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(run).toHaveBeenCalledOnce()
    expect(timer.running.value).toBe(true)
    finish()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(999)
    expect(run).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(run).toHaveBeenCalledTimes(2)
    finish()
  })

  it.each(['disable', 'pagehide', 'unmount'])('cancels the active request on %s', async action => {
    let signal!: AbortSignal
    const run = setup(vi.fn().mockImplementation((value: AbortSignal) => {
      signal = value
      return new Promise<void>(resolve => value.addEventListener('abort', () => resolve()))
    }))
    timer.setIntervalSeconds(1)
    timer.setEnabled(true)
    await vi.advanceTimersByTimeAsync(1000)
    if (action === 'disable') timer.setEnabled(false)
    if (action === 'pagehide') window.dispatchEvent(new Event('pagehide'))
    if (action === 'unmount') wrapper.unmount()
    expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(5000)
    expect(run).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('pauses in the background and resumes without catch-up requests', async () => {
    const run = setup()
    timer.setIntervalSeconds(3)
    timer.setEnabled(true)
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(10000)
    expect(run).not.toHaveBeenCalled()
    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(2999)
    expect(run).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(run).toHaveBeenCalledOnce()
  })

  it('pauses for manual operations and retries a failed round without an unhandled rejection', async () => {
    const busy = ref(true)
    const run = setup(vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(undefined), () => busy.value)
    timer.setIntervalSeconds(1)
    timer.setEnabled(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(run).not.toHaveBeenCalled()
    busy.value = false
    await vi.advanceTimersByTimeAsync(1000)
    expect(timer.failed.value).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    expect(run).toHaveBeenCalledTimes(2)
    expect(timer.failed.value).toBe(false)
  })
})
