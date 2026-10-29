import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { useI18n } from 'vue-i18n'
import i18n, { getLocale, initI18n, loadLocaleMessages, setLocale } from '../index'

vi.mock('@/router/title', () => ({ resolveRouteDocumentTitle: () => 'Localized title' }))
vi.mock('@/router', () => ({ default: { currentRoute: { value: { meta: {} } } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ siteName: 'Test', cachedPublicSettings: null }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: false }) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))

describe('production i18n runtime compatibility', () => {
  afterEach(() => {
    localStorage.removeItem('sub2api_locale')
    document.documentElement.removeAttribute('lang')
    document.title = ''
    i18n.global.locale.value = 'en'
  })

  it('loads real locale modules and updates composition/global translations when switching languages', async () => {
    await initI18n()
    await setLocale('en')
    const view = mount(defineComponent({
      setup() {
        const { t } = useI18n()
        return () => h('p', t('common.peakRateTooltip', { window: '09:00–17:00' }))
      }
    }), { global: { plugins: [i18n] } })
    try {
      expect(view.text()).toBe('Peak rate: 09:00–17:00')
      expect(i18n.global.t('common.save')).toBe('Save')
      expect(i18n.global.t('batchImage.list.selectedJobs', { count: 1 }, 1)).toBe('Selected 1 job')
      expect(i18n.global.t('batchImage.list.selectedJobs', { count: 2 }, 2)).toBe('Selected 2 jobs')
      await setLocale('zh')
      await nextTick()
      expect(getLocale()).toBe('zh')
      expect(view.text()).toBe('高峰倍率：09:00–17:00')
      expect(i18n.global.t('common.save')).toBe('保存')
      expect(localStorage.getItem('sub2api_locale')).toBe('zh')
      expect(document.documentElement.lang).toBe('zh')
      expect(document.title).toBe('Localized title')
      await setLocale('invalid')
      expect(getLocale()).toBe('zh')
      await setLocale('en')
      await nextTick()
      expect(view.text()).toBe('Peak rate: 09:00–17:00')
      await loadLocaleMessages('en')
      expect(i18n.global.t('common.save')).toBe('Save')
    } finally {
      view.unmount()
    }
  })
})
