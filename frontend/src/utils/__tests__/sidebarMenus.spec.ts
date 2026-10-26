import { describe, expect, it } from 'vitest'
import { isSidebarMenuVisible, normalizeSidebarMenuVisibility, SIDEBAR_MENU_ITEMS } from '../sidebarMenus'

describe('sidebar menu visibility', () => {
  it('hides configurable menus before settings load or when settings are missing', () => {
    for (const settings of [undefined, null, {}]) {
      for (const item of SIDEBAR_MENU_ITEMS) {
        expect(isSidebarMenuVisible(settings, item.path), item.path).toBe(false)
      }
      expect(isSidebarMenuVisible(settings, '/admin/settings')).toBe(true)
      expect(isSidebarMenuVisible(settings, '/keys')).toBe(true)
    }
  })

  it('only shows explicitly enabled menus, independently for admin and personal entries', () => {
    const visibility = normalizeSidebarMenuVisibility()
    const settings = { sidebar_menu_visibility: visibility }
    for (const item of SIDEBAR_MENU_ITEMS) {
      visibility[item.key] = true
      for (const candidate of SIDEBAR_MENU_ITEMS) {
        expect(isSidebarMenuVisible(settings, candidate.path)).toBe(candidate.key === item.key)
      }
      visibility[item.key] = false
      expect(isSidebarMenuVisible(settings, item.path)).toBe(false)
    }
  })

  it('fills missing values with off and copies saved settings without sharing state', () => {
    const saved = { channels: true, profile: false }
    const normalized = normalizeSidebarMenuVisibility(saved)
    expect(normalized.channels).toBe(true)
    expect(normalized.profile).toBe(false)
    expect(normalized.redeem).toBe(false)
    normalized.channels = false
    expect(saved.channels).toBe(true)
  })
})
