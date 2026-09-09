import { reactive } from 'vue'
import { Channel, invoke } from '@tauri-apps/api/core'

export type AppUpdateInfo = { version: string; notes: string; url: string; size: number }
// Kept outside the settings dialog so closing it does not lose a download.
export const appUpdate = reactive({
  phase: 'idle' as 'idle' | 'checking' | 'available' | 'current' | 'downloading' | 'ready' | 'installing',
  info: null as AppUpdateInfo | null,
  downloaded: 0, total: 0, error: '',
})
let startupUpdateStarted = false
/** One background attempt per launch; installation always requires a click. */
export async function prepareStartupUpdate() {
  if (startupUpdateStarted) return
  startupUpdateStarted = true
  // Respect a manual check/download that the user already started.
  if (appUpdate.phase !== 'idle') return
  await checkAppUpdate()
  if (['available'].includes(appUpdate.phase) && !appUpdate.error) await downloadAppUpdate()
}
export async function checkAppUpdate() {
  if (['checking', 'downloading', 'installing'].includes(appUpdate.phase)) return
  const previous = appUpdate.phase
  appUpdate.phase = 'checking'; appUpdate.error = ''
  try {
    appUpdate.info = await invoke<AppUpdateInfo | null>('check_app_update')
    appUpdate.phase = appUpdate.info ? 'available' : 'current'
  } catch (error) { appUpdate.error = String(error); appUpdate.phase = previous }
}
export async function downloadAppUpdate() {
  if (!appUpdate.info || !['available', 'ready'].includes(appUpdate.phase)) return
  appUpdate.phase = 'downloading'; appUpdate.error = ''; appUpdate.downloaded = 0; appUpdate.total = appUpdate.info.size
  const channel = new Channel<{ downloaded: number; total: number }>()
  channel.onmessage = progress => { appUpdate.downloaded = progress.downloaded; appUpdate.total = progress.total }
  try { await invoke('download_app_update', { onProgress: channel }); appUpdate.phase = 'ready' }
  catch (error) { appUpdate.error = String(error); appUpdate.phase = 'available' }
}
export async function installAppUpdate() {
  if (appUpdate.phase !== 'ready') return
  appUpdate.phase = 'installing'; appUpdate.error = ''
  try { await invoke('install_app_update') }
  catch (error) { appUpdate.error = String(error); appUpdate.phase = 'ready' }
}
