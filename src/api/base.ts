// sidecar 基址解析。
// - 开发模式（vite dev）：连 127.0.0.1:17654（LAUNCHER_PORT 默认），需先手动 go run sidecar
// - Tauri 壳：壳启动 sidecar 后通过 window.__LAUNCHER_BASE__ 注入基址
// 通过运行期探测 /api/health 判断 sidecar 是否就绪

const DEV_BASE = 'http://127.0.0.1:17654'

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
    clearTimeout(t)
    return r.ok
  } catch {
    clearTimeout(t)
    return false
  }
}
