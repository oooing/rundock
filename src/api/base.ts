import { tr } from '@/i18n'

// sidecar 基址解析。
// - 开发模式（vite dev）：连独立后端 127.0.0.1:17655
// - Tauri 壳：壳启动 sidecar 后通过 window.__LAUNCHER_BASE__ 注入基址
// 通过运行期探测 /api/health 判断 sidecar 是否就绪

const DEV_BASE = (import.meta.env.VITE_LAUNCHER_BASE as string | undefined) ||
  (import.meta.env.DEV ? 'http://127.0.0.1:17655' : 'http://127.0.0.1:17654')

export class IncompatibleSidecarError extends Error {}

export function getBaseURL(): string {
  // Tauri 壳注入的基址优先
  const injected = (window as any).__LAUNCHER_BASE__ as string | undefined
  if (injected) return injected.replace(/\/$/, '')
  return DEV_BASE
}

export function getWsURL(): string {
  const base = getBaseURL()
  const wsBase = base.replace(/^http/, 'ws')
  return wsBase + '/ws'
}

/** 探测 sidecar 是否就绪 */
export async function pingSidecar(timeoutMs = 2000): Promise<boolean> {
  const ctrl = new AbortController()
  const t = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const r = await fetch(`${getBaseURL()}/api/health`, { signal: ctrl.signal })
    if (!r.ok) return false
    const health = await r.json() as { apiVersion?: string; capabilities?: string }
    if (health.apiVersion !== '2' || health.capabilities !== 'release-v2') {
      throw new IncompatibleSidecarError(tr("检测到旧版 sidecar。请关闭旧的 Launcher-Backend 窗口，然后重新运行 scripts/dev.bat。"))
    }
    return true
  } catch (error) {
    if (error instanceof IncompatibleSidecarError) throw error
    return false
  } finally {
    clearTimeout(t)
  }
}
