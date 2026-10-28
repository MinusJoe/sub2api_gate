export const PROXY_AUTO_TEST_TARGETS_KEY = 'admin:proxies:auto-test:targets'
let sessionTargets: number[] = []

function normalize(value: unknown): number[] {
  if (!Array.isArray(value)) return []
  return [...new Set(value.filter((id): id is number => Number.isSafeInteger(id) && id > 0))]
}

export function saveProxyAutoTestTargets(ids: number[]): void {
  sessionTargets = normalize(ids)
  try { localStorage.setItem(PROXY_AUTO_TEST_TARGETS_KEY, JSON.stringify(sessionTargets)) } catch { /* Keep the in-session selection. */ }
}

export function readProxyAutoTestTargets(): number[] {
  let saved: string | null
  try {
    saved = localStorage.getItem(PROXY_AUTO_TEST_TARGETS_KEY)
  } catch {
    return [...sessionTargets]
  }
  try { return saved === null ? [] : normalize(JSON.parse(saved)) } catch { return [] }
}
