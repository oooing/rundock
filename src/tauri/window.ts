// Tauri 窗口/事件能力封装。集中隔离平台差异：
// - 打包后（Tauri 壳）：真实调用 @tauri-apps/api
// - 开发模式（vite dev，无壳）：降级为 no-op / 浏览器行为，避免 import 失败
//
// 全项目首次引入 @tauri-apps/api，统一从此文件导出，其它地方不直接 import。

import { getCurrentWindow } from '@tauri-apps/api/window'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { invoke } from '@tauri-apps/api/core'
import { getVersion } from '@tauri-apps/api/app'
import pkg from '../../package.json'

/** 是否看起来运行在 Tauri 壳内。只用于 UI 提示，不再作为调用 Tauri API 的硬门禁。 */
export const isTauri =
  !!(window as any).__TAURI_INTERNALS__ ||
  !!(window as any).__TAURI__ ||
  window.location.protocol === 'tauri:' ||
  window.location.hostname === 'tauri.localhost'

/** 隐藏主窗口（最小化到托盘）。非 Tauri 环境为 no-op。 */
export async function hideMainWindow(): Promise<void> {
  await getCurrentWindow().hide().catch(() => {})
}

/** 显示并聚焦主窗口。非 Tauri 环境为 no-op。 */
export async function showMainWindow(): Promise<void> {
  const w = getCurrentWindow()
  await w.unminimize().catch(() => {})
  await w.show().catch(() => {})
  await w.setFocus().catch(() => {})
}

/** 退出应用（quit_app command：先停止所有项目服务，再退出）。非 Tauri 环境为 no-op。 */
export async function quitApp(): Promise<void> {
  await invoke('quit_app')
}

/** 应用版本号。Tauri 中读取打包版本；开发浏览器中回退到 package.json。 */
export async function getAppVersion(): Promise<string> {
  return getVersion().catch(() => pkg.version)
}

/**
 * 监听后端 emit 的事件，返回卸载函数。非 Tauri 环境返回 no-op。
 * 用于 close-requested（点 X）和 tray-quit-requested（托盘右键退出）。
 */
export async function onTauriEvent<T = unknown>(
  name: string,
  handler: (payload: T) => void,
): Promise<UnlistenFn | null> {
  return listen<T>(name, (e) => handler(e.payload)).catch(() => null)
}
