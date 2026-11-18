import type { TLSFingerprintStatus } from '@/types'

export function tlsFingerprintLabel(status?: TLSFingerprintStatus | null): string {
  switch (status) {
    case 'applied': return 'admin.usage.tlsApplied'
    case 'standard': return 'admin.usage.tlsStandard'
    case 'plain': return 'admin.usage.tlsPlain'
    case 'plugin': return 'admin.usage.tlsPlugin'
    default: return 'admin.usage.tlsUnknown'
  }
}
