import { reactive } from 'vue'
import { Channel, invoke } from '@tauri-apps/api/core'

export type AppUpdateInfo = { version: string; notes: string; url: string; size: number; installedLocally?: boolean }
export type UpdateProject = { id: string; name: string; runId: string; configHash: string; temporaryPort?: number }
type UpdatePlan = { fingerprint: string; projects: UpdateProject[] }
type UpdateReport = { version: string; phase: string; projects: UpdateProject[]; remaining: UpdateProject[]; error: string; failures: string[] }
export const appUpdate = reactive({
  phase: 'idle' as 'idle' | 'checking' | 'available' | 'current' | 'downloading' | 'ready' | 'planning' | 'confirming' | 'scheduled' | 'installing' | 'installed' | 'restoring' | 'finished',
  info: null as AppUpdateInfo | null,
  downloaded: 0, total: 0, error: '', dialogOpen: false,
  plan: null as UpdatePlan | null, report: null as UpdateReport | null,
})
const busy = () => ['checking', 'downloading', 'planning', 'installing', 'restoring'].includes(appUpdate.phase)
export function openAppUpdate() { if (appUpdate.info) appUpdate.dialogOpen = true }
export function dismissAppUpdate() { if (!['installing','planning'].includes(appUpdate.phase)) appUpdate.dialogOpen = false }
let startupUpdateStarted = false
let reportTimer: ReturnType<typeof setTimeout> | undefined
export async function refreshUpdateStatus() {
  const report = await invoke<UpdateReport | null>('app_update_status')
  if (!report) return false
  appUpdate.report = report
  appUpdate.info = { version: report.version, notes: '', url: '', size: 0 }
  appUpdate.phase = report.phase === 'scheduled' ? 'scheduled' : report.phase === 'finished' ? 'finished' : 'restoring'
  appUpdate.error = report.error
  appUpdate.dialogOpen = report.phase !== 'scheduled'
  if (reportTimer) clearTimeout(reportTimer)
  if (appUpdate.phase === 'restoring') reportTimer = setTimeout(() => { void refreshUpdateStatus().catch(e => { appUpdate.error = String(e) }) }, 1000)
  return true
}
export async function prepareStartupUpdate() {
  if (startupUpdateStarted) return
  startupUpdateStarted = true
  if (appUpdate.phase !== 'idle') return
  try { if (await refreshUpdateStatus()) return }
  catch (e) { appUpdate.error = String(e); return }
  await checkAppUpdate()
  if (['available'].includes(appUpdate.phase) && !appUpdate.error) await downloadAppUpdate()
}
export async function checkAppUpdate() {
  if (busy()) return
  if (['ready','installed','scheduled','confirming','finished'].includes(appUpdate.phase)) { openAppUpdate(); return }
  const previous = appUpdate.phase
  appUpdate.phase = 'checking'; appUpdate.error = ''
  try {
    appUpdate.info = await invoke<AppUpdateInfo | null>('check_app_update')
    appUpdate.phase = appUpdate.info?.installedLocally ? 'installed' : appUpdate.info ? 'available' : 'current'
    appUpdate.dialogOpen = !!appUpdate.info
  } catch (error) { appUpdate.error = String(error); appUpdate.phase = previous }
}
export async function downloadAppUpdate() {
  if (!appUpdate.info || !['available','ready'].includes(appUpdate.phase)) return
  appUpdate.phase = 'downloading'; appUpdate.error = ''; appUpdate.downloaded = 0; appUpdate.total = appUpdate.info.size
  const channel = new Channel<{ downloaded: number; total: number }>()
  channel.onmessage = p => { appUpdate.downloaded = p.downloaded; appUpdate.total = p.total }
  try { await invoke('download_app_update', { onProgress: channel }); appUpdate.phase = 'ready' }
  catch (error) { appUpdate.error = String(error); appUpdate.phase = 'available' }
}
export async function scheduleAppUpdate() {
  if (!['ready','installed','confirming'].includes(appUpdate.phase)) return
  const previous = appUpdate.phase; appUpdate.phase = 'planning'; appUpdate.error = ''
  try { await invoke('schedule_app_update'); appUpdate.phase = 'scheduled'; appUpdate.plan = null }
  catch (e) { appUpdate.phase = previous; appUpdate.error = String(e) }
}
export async function planAppUpdate() {
  if (!['ready','scheduled','installed','confirming'].includes(appUpdate.phase)) return
  const previous = appUpdate.phase; appUpdate.phase = 'planning'; appUpdate.error = ''; appUpdate.plan = null
  try { appUpdate.plan = await invoke<UpdatePlan>('update_install_plan'); appUpdate.phase = 'confirming' }
  catch (e) { appUpdate.phase = previous === 'confirming' ? 'ready' : previous; appUpdate.error = String(e) }
}
export async function installAppUpdate() {
  if (appUpdate.phase !== 'confirming' || !appUpdate.plan) return
  const fingerprint = appUpdate.plan.fingerprint
  appUpdate.phase = 'installing'; appUpdate.error = ''
  try { await invoke('install_app_update', { fingerprint }) }
  catch (e) {
    appUpdate.phase = 'ready'; appUpdate.plan = null; appUpdate.error = String(e)
    try { await refreshUpdateStatus() } catch { /* retain the installation error */ }
  }
}
export async function cancelScheduledUpdate() {
  if (appUpdate.phase !== 'scheduled' && appUpdate.phase !== 'finished') return
  try { await invoke('cancel_scheduled_update'); appUpdate.phase = 'idle'; appUpdate.info = null; appUpdate.report = null; appUpdate.dialogOpen = false }
  catch (e) { appUpdate.error = String(e) }
}
export async function recheckAppUpdate() {
  await cancelScheduledUpdate()
  if (appUpdate.phase === 'idle') await checkAppUpdate()
}
export async function retryUpdateRestore() {
  if (appUpdate.phase !== 'finished' || !appUpdate.report?.remaining.length) return
  appUpdate.phase = 'restoring'; appUpdate.error = ''
  try { await invoke('retry_update_restore'); await refreshUpdateStatus() }
  catch (e) { appUpdate.phase = 'finished'; appUpdate.error = String(e) }
}
