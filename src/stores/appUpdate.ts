import { computed, reactive } from 'vue'
import { Channel, invoke } from '@tauri-apps/api/core'

export type AppUpdateInfo = { version: string; notes: string; url: string; size: number; installedLocally?: boolean }
export type UpdateProject = { id: string; name: string; runId: string; configHash: string; temporaryPort?: number }
type UpdatePlan = { fingerprint: string; projects: UpdateProject[] }
type UpdateReport = { version: string; phase: string; projects: UpdateProject[]; remaining: UpdateProject[]; error: string; failures: string[]; installer?: unknown; currentVersion?: string; backendVersion?: string; versionActive?: boolean; statusError?: string; activationState?: 'restart_required' | 'backend_pending' | 'active' | 'unconfirmed' }
type UpdateOptions = { background?: boolean }
export const appUpdate = reactive({
  phase: 'idle' as 'idle' | 'checking' | 'available' | 'current' | 'downloading' | 'ready' | 'staging' | 'planning' | 'confirming' | 'scheduled' | 'installing' | 'installed' | 'restoring' | 'finished',
  info: null as AppUpdateInfo | null,
  downloaded: 0, total: 0, error: '', dialogOpen: false,
  plan: null as UpdatePlan | null, report: null as UpdateReport | null,
})
// One explanation for the sidebar, settings and dialog. Installed files do not
// imply that the retained backend has switched versions.
export const installedUpdateCopy = computed(() => {
  switch (appUpdate.report?.activationState) {
    case 'backend_pending': return { title: '后台待更新', short: '窗口已更新', message: '窗口已更新，项目继续运行；后台暂用旧版。', hint: '只退出窗口不会更新后台；立即生效前会列出要重启的项目。', canActivate: true }
    case 'unconfirmed': return { title: '状态待确认', short: '后台状态待确认', message: '暂时无法确认后台版本，项目保持运行。', hint: '稍后打开此窗口会自动核对，不会重复安装。', canActivate: false }
    case 'active': return { title: '更新完成', short: '新版已生效', message: '窗口和后台均已更新，项目保持原状。', hint: '', canActivate: false }
    default: return { title: '新版已安装', short: '等待完整重启', message: '新版已安装，完整重启后生效。项目暂不受影响。', hint: '保留项目退出只会更新窗口；后台更新需停止项目。', canActivate: true }
  }
})
let startupUpdateStarted = false
let stagePending = false
let cancelPending = false
let confirmationPhase: 'ready' | 'scheduled' | 'installed' = 'ready'
let reportTimer: ReturnType<typeof setTimeout> | undefined
let statusRevision = 0
let statusRequest: { revision: number; promise: Promise<boolean> } | undefined
const busy = () => stagePending || cancelPending || ['checking', 'downloading', 'staging', 'planning', 'installing', 'restoring'].includes(appUpdate.phase)
const hasInstalledRecord = () => appUpdate.phase === 'installed' && appUpdate.report?.phase === 'staged'
const waitingForStatus = () => ['staging', 'restoring'].includes(appUpdate.phase) || ((hasInstalledRecord() || appUpdate.phase === 'finished') && appUpdate.dialogOpen)
function clearReportTimer() {
  if (reportTimer !== undefined) clearTimeout(reportTimer)
  reportTimer = undefined
}
function invalidateStatus() { statusRevision++; clearReportTimer() }
function pollStatus() {
  if (!waitingForStatus() || reportTimer !== undefined) return
  reportTimer = setTimeout(() => {
    reportTimer = undefined
    void refreshUpdateStatus({ background: true }).catch(e => { appUpdate.error = String(e) })
  }, 1000)
}
export function openAppUpdate() {
  if (appUpdate.info || appUpdate.error) appUpdate.dialogOpen = true
  if (hasInstalledRecord() || appUpdate.phase === 'finished') {
    void refreshUpdateStatus({ background: true }).catch(e => { appUpdate.error = String(e) })
  }
}
export function cancelAppUpdatePlan() {
  if (appUpdate.phase !== 'confirming') return
  appUpdate.phase = appUpdate.info?.installedLocally ? 'installed' : confirmationPhase
  appUpdate.plan = null
}
export function dismissAppUpdate() {
  if (['installing', 'planning'].includes(appUpdate.phase)) return
  cancelAppUpdatePlan()
  appUpdate.dialogOpen = false
  if (['installed', 'finished'].includes(appUpdate.phase)) clearReportTimer()
}
export function refreshUpdateStatus({ background = false }: UpdateOptions = {}): Promise<boolean> {
  if (statusRequest?.revision === statusRevision) return statusRequest.promise
  clearReportTimer()
  const revision = statusRevision
  const promise = (async () => {
    try {
      const report = await invoke<UpdateReport | null>('app_update_status')
      if (revision !== statusRevision) return false
      if (!report) {
        // A verified obsolete result can be archived by the native layer.
        if (hasInstalledRecord() || appUpdate.phase === 'finished') {
          appUpdate.report = null; appUpdate.info = null; appUpdate.error = ''
          appUpdate.phase = 'current'
        }
        return false
      }
      const previousInfo = appUpdate.info?.version === report.version ? appUpdate.info : null
      // Older schedules may already refer to an installed version (no installer).
      const installedLocally = report.phase === 'staged' || (report.phase === 'scheduled' ? report.installer === null : previousInfo?.installedLocally === true)
      appUpdate.report = report
      appUpdate.info = { version: report.version, notes: '', url: '', size: 0, ...previousInfo, installedLocally }
      appUpdate.phase = report.phase === 'staged' ? 'installed'
        : report.phase === 'staging' ? 'staging'
        : report.phase === 'scheduled' ? (installedLocally ? 'installed' : 'scheduled')
        : report.phase === 'finished' ? 'finished' : 'restoring'
      appUpdate.error = report.error || (report.phase === 'staging' ? appUpdate.error : '')
      if (!background && ['restoring', 'finished'].includes(appUpdate.phase)) appUpdate.dialogOpen = true
      return true
    } catch (e) {
      if (revision !== statusRevision) return false
      throw e
    } finally {
      if (statusRequest?.revision === revision) statusRequest = undefined
      if (revision === statusRevision) pollStatus()
    }
  })()
  statusRequest = { revision, promise }
  return promise
}
export async function prepareStartupUpdate() {
  if (startupUpdateStarted) return
  startupUpdateStarted = true
  if (appUpdate.phase !== 'idle') return
  await checkAppUpdate({ background: true })
  if (['available'].includes(appUpdate.phase) && !appUpdate.error) {
    await downloadAppUpdate()
    if (['ready'].includes(appUpdate.phase) && !appUpdate.error) await stageAppUpdate()
  }
}
export async function checkAppUpdate({ background = false }: UpdateOptions = {}) {
  if (busy()) return
  if (['ready', 'installed', 'scheduled', 'confirming', 'finished'].includes(appUpdate.phase)) {
    if (!background) openAppUpdate()
    return
  }
  const previous = appUpdate.phase
  invalidateStatus()
  appUpdate.phase = 'checking'; appUpdate.error = ''
  try {
    // Reconcile persisted work before checking again, including after a status-read failure.
    if (!await refreshUpdateStatus({ background: true })) {
      appUpdate.info = await invoke<AppUpdateInfo | null>('check_app_update')
      appUpdate.phase = appUpdate.info?.installedLocally ? 'installed' : appUpdate.info ? 'available' : 'current'
    }
    if (!background) openAppUpdate()
  } catch (error) { appUpdate.error = String(error); appUpdate.phase = previous }
}
export async function downloadAppUpdate() {
  if (busy() || !appUpdate.info || !['available', 'ready'].includes(appUpdate.phase)) return
  invalidateStatus()
  appUpdate.phase = 'downloading'; appUpdate.error = ''; appUpdate.downloaded = 0; appUpdate.total = appUpdate.info.size
  const channel = new Channel<{ downloaded: number; total: number }>()
  channel.onmessage = p => { appUpdate.downloaded = p.downloaded; appUpdate.total = p.total }
  try { await invoke('download_app_update', { onProgress: channel }); appUpdate.phase = 'ready' }
  catch (error) { appUpdate.error = String(error); appUpdate.phase = 'available' }
}
export async function stageAppUpdate() {
  if (busy() || !appUpdate.info || appUpdate.info.installedLocally || !['ready', 'scheduled', 'confirming'].includes(appUpdate.phase)) return
  const previous = appUpdate.phase === 'confirming' ? confirmationPhase : appUpdate.phase
  stagePending = true
  invalidateStatus()
  appUpdate.phase = 'staging'; appUpdate.error = ''; appUpdate.plan = null
  pollStatus()
  try {
    await invoke<void>('stage_app_update')
    // The void success confirms installation; an earlier status read must not undo it.
    invalidateStatus()
    appUpdate.phase = 'installed'; appUpdate.info.installedLocally = true
    appUpdate.error = ''; appUpdate.report = null
  } catch (e) {
    invalidateStatus()
    appUpdate.error = String(e)
    try {
      if (!await refreshUpdateStatus({ background: true })) appUpdate.phase = previous
    } catch { /* Unknown outcome: keep staging and poll, never submit another installer. */ }
    if (appUpdate.phase !== 'installed' && !appUpdate.error) appUpdate.error = String(e)
  } finally {
    stagePending = false
    clearReportTimer()
    pollStatus()
  }
}
// Keep callers of the former next-start action on the non-disruptive path.
export const scheduleAppUpdate = stageAppUpdate
export async function planAppUpdate() {
  if (busy() || !['installed', 'confirming'].includes(appUpdate.phase) || appUpdate.info?.installedLocally !== true || (appUpdate.phase === 'installed' && !installedUpdateCopy.value.canActivate)) return
  if (appUpdate.phase !== 'confirming') confirmationPhase = appUpdate.phase as typeof confirmationPhase
  invalidateStatus()
  appUpdate.phase = 'planning'; appUpdate.error = ''; appUpdate.plan = null
  try { appUpdate.plan = await invoke<UpdatePlan>('update_install_plan'); appUpdate.phase = 'confirming' }
  catch (e) { appUpdate.phase = confirmationPhase; appUpdate.error = String(e) }
}
export async function installAppUpdate() {
  if (busy() || appUpdate.phase !== 'confirming' || appUpdate.info?.installedLocally !== true || !appUpdate.plan) return
  const fingerprint = appUpdate.plan.fingerprint
  invalidateStatus()
  appUpdate.phase = 'installing'; appUpdate.error = ''
  try { await invoke('install_app_update', { fingerprint }) }
  catch (e) {
    appUpdate.phase = confirmationPhase; appUpdate.plan = null; appUpdate.error = String(e)
    try { await refreshUpdateStatus({ background: true }) } catch { /* retain the installation error */ }
    if (!appUpdate.error && !appUpdate.report?.versionActive) appUpdate.error = String(e)
  }
}
export async function cancelScheduledUpdate(keepCurrentState = false) {
  if (busy() || !['scheduled', 'finished'].includes(appUpdate.phase)) return
  cancelPending = true
  invalidateStatus()
  try {
    await invoke('cancel_scheduled_update', keepCurrentState === true ? { keepCurrentState: true } : undefined)
    appUpdate.phase = appUpdate.info?.installedLocally ? 'installed' : 'idle'
    if (appUpdate.phase === 'idle') appUpdate.info = null
    appUpdate.report = null; appUpdate.plan = null; appUpdate.error = ''; appUpdate.dialogOpen = false
  } catch (e) { appUpdate.error = String(e) }
  finally { cancelPending = false }
}
export async function recheckAppUpdate() {
  await cancelScheduledUpdate()
  if (appUpdate.phase === 'idle') await checkAppUpdate()
}
export async function retryUpdateRestore() {
  if (busy() || appUpdate.phase !== 'finished' || !appUpdate.report?.remaining.length) return
  invalidateStatus()
  appUpdate.phase = 'restoring'; appUpdate.error = ''
  try { await invoke('retry_update_restore'); await refreshUpdateStatus({ background: true }) }
  catch (e) { appUpdate.phase = 'finished'; appUpdate.error = String(e) }
}
