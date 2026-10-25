import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIWSModeRouterSettings from '../OpenAIWSModeRouterSettings.vue'

const { get, put, showSuccess, showError } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const endpoint = '/admin/settings/openai-ws-mode-router'
const config = { enabled: false, override: null, source: 'config', config_enabled: false }
const wrappers: Array<{ unmount(): void }> = []
async function render() {
  const wrapper = mount(OpenAIWSModeRouterSettings)
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('OpenAIWSModeRouterSettings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    get.mockResolvedValue({ data: { ...config } })
    put.mockImplementation(async (_path, body) => ({ data: body.enabled === null ? { ...config } : { ...config, enabled: body.enabled, override: body.enabled, source: 'web' } }))
  })
  afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()))

  it('loads inherited configuration without enabling or saving automatically', async () => {
    const wrapper = await render()
    expect(get).toHaveBeenCalledWith(endpoint)
    expect(wrapper.get('[data-testid="ws-mode-router-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="ws-mode-router-source"]').text()).toBe('admin.settings.openaiWSModeRouter.sourceConfig')
    expect(put).not.toHaveBeenCalled()
  })

  it('saves a web override and can restore deployment configuration', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ws-mode-router-toggle"]').trigger('click')
    await wrapper.get('[data-testid="ws-mode-router-save"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenLastCalledWith(endpoint, { enabled: true })
    expect(wrapper.get('[data-testid="ws-mode-router-source"]').text()).toBe('admin.settings.openaiWSModeRouter.sourceWeb')
    await wrapper.get('[data-testid="ws-mode-router-inherit"]').trigger('click')
    await flushPromises()
    expect(put).toHaveBeenLastCalledWith(endpoint, { enabled: null })
    expect(wrapper.get('[data-testid="ws-mode-router-toggle"]').attributes('aria-checked')).toBe('false')
    expect(showSuccess).toHaveBeenCalledTimes(2)
  })

  it('keeps controls unavailable on a failed load and supports retry', async () => {
    get.mockRejectedValueOnce(new Error('read failed'))
    const wrapper = await render()
    expect(wrapper.find('[data-testid="ws-mode-router-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ws-mode-router-save"]').exists()).toBe(false)
    expect(put).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="ws-mode-router-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="ws-mode-router-toggle"]').exists()).toBe(true)
  })

  it('does not show a successful save when persistence fails', async () => {
    put.mockRejectedValueOnce(new Error('write failed'))
    const wrapper = await render()
    await wrapper.get('[data-testid="ws-mode-router-toggle"]').trigger('click')
    await wrapper.get('[data-testid="ws-mode-router-save"]').trigger('click')
    await flushPromises()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('write failed')
    expect(wrapper.get('[data-testid="ws-mode-router-source"]').text()).toBe('admin.settings.openaiWSModeRouter.sourceConfig')
  })
})
