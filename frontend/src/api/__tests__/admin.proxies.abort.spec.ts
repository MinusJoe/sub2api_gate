import { describe, expect, it, vi } from 'vitest'
import { testProxy } from '../admin/proxies'

const post = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { post } }))

describe('proxy test cancellation', () => {
  it('forwards the abort signal to the HTTP request', async () => {
    const controller = new AbortController()
    post.mockResolvedValue({ data: { success: true, latency_ms: 12 } })
    expect(await testProxy(42, { signal: controller.signal })).toEqual({ success: true, latency_ms: 12 })
    expect(post).toHaveBeenCalledWith('/admin/proxies/42/test', undefined, { signal: controller.signal })
  })
})
