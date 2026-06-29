// Tauri 窗口/事件能力封装。集中隔离平台差异：
// - 打包后（Tauri 壳）：真实调用 @tauri-apps/api
// - 开发模式（vite dev，无壳）：降级为 no-op / 浏览器行为，避免 import 失败
//
// 全项目首次引入 @tauri-apps/api，统一从此文件导出，其它地方不直接 import。

import { getCurrentWindow } from '@tauri-apps/api/window'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { invoke } from '@tauri-apps/api/core'

/** 是否运行在 Tauri 壳内（有 __TAURI_INTERNALS__）。开发模式为 false。 */
export const isTauri = !!(window as any).__TAURI_INTERNALS__ || !!(window as any).__TAURI__

/** 隐藏主窗口（最小化到托盘）。非 Tauri 环境为 no-op。 */
export async function hideMainWindow(): Promise<void> {
  if (!isTauri) return
  await getCurrentWindow().hide()
}

/** 显示并聚焦主窗口。非 Tauri 环境为 no-op。 */
export async function showMainWindow(): Promise<void> {
  if (!isTauri) return
  const w = getCurrentWindow()
  await w.show()
  await w.setFocus()
}

/** 退出应用（quit_app command：先停止所有项目服务，再退出）。非 Tauri 环境为 no-op。 */
export async function quitApp(): Promise<void> {
  if (!isTauri) return
  await invoke('quit_app')
}

/**
 * 监听后端 emit 的事件，返回卸载函数。非 Tauri 环境返回 no-op。
 * 用于 close-requested（点 X）和 tray-quit-requested（托盘右键退出）。
 */
export async function onTauriEvent<T = unknown>(
  name: string,
  handler: (payload: T) => void,
): Promise<UnlistenFn | null> {
  if (!isTauri) return null
  return listen<T>(name, (e) => handler(e.payload))
}
