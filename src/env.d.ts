/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface Window {
  /** Tauri 壳注入的 sidecar 基址，例如 http://127.0.0.1:17654 */
  __LAUNCHER_BASE__?: string
}
