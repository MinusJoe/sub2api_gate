import type { PublicSettings, SidebarMenuVisibility } from '@/types'

export const SIDEBAR_MENU_ITEMS = [
  { key: 'channels', path: '/admin/channels', labelKey: 'nav.channelManagement' },
  { key: 'announcements', path: '/admin/announcements', labelKey: 'nav.announcements' },
  { key: 'redeem_codes', path: '/admin/redeem', labelKey: 'nav.redeemCodes' },
  { key: 'promo_codes', path: '/admin/promo-codes', labelKey: 'nav.promoCodes' },
  { key: 'admin_subscriptions', path: '/admin/subscriptions', labelKey: 'nav.subscriptions' },
  { key: 'subscriptions', path: '/subscriptions', labelKey: 'nav.mySubscriptions' },
  { key: 'redeem', path: '/redeem', labelKey: 'nav.redeem' },
  { key: 'profile', path: '/profile', labelKey: 'nav.profile' },
] as const

export function normalizeSidebarMenuVisibility(
  value?: Partial<SidebarMenuVisibility> | null,
): SidebarMenuVisibility {
  return {
    channels: value?.channels === true,
    announcements: value?.announcements === true,
    redeem_codes: value?.redeem_codes === true,
    promo_codes: value?.promo_codes === true,
    admin_subscriptions: value?.admin_subscriptions === true,
    subscriptions: value?.subscriptions === true,
    redeem: value?.redeem === true,
    profile: value?.profile === true,
  }
}

export function isSidebarMenuVisible(
  settings: Pick<PublicSettings, 'sidebar_menu_visibility'> | null | undefined,
  path: string,
): boolean {
  const item = SIDEBAR_MENU_ITEMS.find((entry) => entry.path === path)
  return !item || settings?.sidebar_menu_visibility?.[item.key] === true
}
