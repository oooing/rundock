// HTTP 客户端：所有 REST 调用集中在此，返回类型化结果。
import { getBaseURL } from './base'
import { tr } from '@/i18n'
import type {
  AppView,
  StartupIssue,
  PortResolution,
  CreateAppBody,
  ExportSnapshot,
  Group,
  ImportCandidate,
  LogsResponse,
  PortEntry,
  ScriptConfirmationResponse,
  ServiceRole,
  StartResponse,
  CreateReleaseBody,
  ReleaseCandidate,
  ReleaseCandidateRequest,
  ReleaseConfig,
  ReleaseNotesDraft,
  ReleaseNotesDraftRequest,
  ReleasePreflight,
  ReleaseProfile,
  ReleaseRun,
  ReleaseRunView,
} from '@/types'

function normalizeReleaseCandidate(view: ReleaseCandidate): ReleaseCandidate {
 return {...view,classifications:view.classifications||[],selectedPaths:view.selectedPaths||[],warnings:view.warnings||[],sensitiveFindings:view.sensitiveFindings||[],dependencyFindings:view.dependencyFindings||[],checkResults:view.checkResults||[]}
}

export class ApiError extends Error {
  constructor(message: string, public code = '', public preflight?: ReleasePreflight) {
    super(message)
    this.name = 'ApiError'
  }
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(`${getBaseURL()}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    let code = ''
    let preflight: ReleasePreflight | undefined
    try {
      const body = await r.json()
      msg = body.code === 'script_confirmation_required' ? '启动脚本已变化，请先点击启动确认新配置' : body.error || msg
      code = typeof body.code === 'string' ? body.code : ''
      preflight = body.preflight
    } catch {
      /* ignore */
    }
    throw new ApiError(tr(msg), code, preflight)
  }
  return r.json() as Promise<T>
}

/**
 * 启动/重启专用请求：可能返回 409（脚本风险变化需确认）。
 * - 200：返回 { ok: true, data: StartResponse }
 * - 409：返回 { ok: false, confirmation: ScriptConfirmationResponse }（不抛错）
 * - 其它错误：抛 Error
 *
 * confirmedScriptHash 由前端在用户确认风险后回带，让后端校验哈希是否仍匹配。
 */
async function startReq(
  path: string,
  confirmedScriptHash?: string,
): Promise<{ ok: true; data: StartResponse } | { ok: false; confirmation: ScriptConfirmationResponse }> {
  const init: RequestInit = { method: 'POST' }
  if (confirmedScriptHash) {
    init.body = JSON.stringify({ confirmedScriptHash })
  }
  const r = await fetch(`${getBaseURL()}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (r.status === 409) {
    const body = await r.json()
    if (body.code === 'script_confirmation_required') return { ok: false, confirmation: body }
    throw new Error(tr(body.error || 'HTTP 409'))
  }
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try {
      const body = await r.json()
      msg = body.error || msg
    } catch {
      /* ignore */
    }
    throw new Error(tr(msg))
  }
  const data = (await r.json()) as StartResponse
  return { ok: true, data }
}

export const api = {
  restartPlan: async (id: string, signal?: AbortSignal) => {
    try { return await req<import('@/utils/restart').RestartPlan>(`/api/apps/${id}/restart-plan`, { method: 'POST', signal }) }
    catch (error) { if (error instanceof Error && /404|not found/i.test(error.message)) throw new Error(tr('当前后台尚未更新，请退出并用新版启动器打开 RunDock 一次，再使用重启。')); throw error }
  },
  confirmRestart: (id: string, confirmationToken: string) => req<StartResponse & { restarting?: boolean; instanceId?: string }>(`/api/apps/${id}/restart-confirm`, { method: 'POST', body: JSON.stringify({ confirmationToken }) }),
  restartHealth: (signal: AbortSignal) => req<{ instanceId?: string; status?: string; apiVersion?: string }>('/api/health', { signal, cache: 'no-store' }),
  checkRuntime: (id: string) => req<AppView>(`/api/apps/${id}/runtime-check`, { method: 'POST' }),
  discoverStartup: (path: string, signal?: AbortSignal) => req<import('@/types').StartupDiscovery>('/api/import/discover', { method: 'POST', body: JSON.stringify({ path }), signal }),
  // 导入（只读分析）
  import: (scriptPath: string, signal?: AbortSignal) =>
    req<ImportCandidate>('/api/import', {
      method: 'POST',
      body: JSON.stringify({ scriptPath }),
      signal,
    }),

  // Apps
  listApps: (signal?: AbortSignal) => req<AppView[]>('/api/apps', { signal }),
  getApp: (id: string) => req<AppView>(`/api/apps/${id}`),
  startupIssue: (id: string) => req<StartupIssue | null>(`/api/apps/${id}/startup-issue`),
  portResolution: (id: string, signal?: AbortSignal) => req<PortResolution>(`/api/apps/${id}/port-resolution`, { method: 'POST', signal }),
  resolvePorts: (id: string, confirmationToken: string) => req<StartResponse>(`/api/apps/${id}/resolve-ports`, { method: 'POST', body: JSON.stringify({ confirmationToken }) }),
  recoverPorts: (id: string, fingerprint: string) => req<StartResponse>(`/api/apps/${id}/recover-ports`, { method: 'POST', body: JSON.stringify({ fingerprint }) }),
  createApp: (body: CreateAppBody) =>
    req<AppView>('/api/apps', { method: 'POST', body: JSON.stringify(body) }),
  updateApp: (id: string, body: Record<string, unknown>) =>
    req<AppView>(`/api/apps/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  reorderApps: (order: string[]) =>
    req<{ updated: number }>('/api/apps/reorder', { method: 'PATCH', body: JSON.stringify({ order }) }),
  deleteApp: (id: string) => req<{ deleted: boolean }>(`/api/apps/${id}`, { method: 'DELETE' }),

  // 操作
  // 注意：start/restart 走 startReq，可能返回 409（脚本风险变化需确认）。
  start: (id: string, confirmedScriptHash?: string) =>
    startReq(`/api/apps/${id}/start`, confirmedScriptHash),
  stop: (id: string) => req<{ stopped: boolean }>(`/api/apps/${id}/stop`, { method: 'POST' }),
  restart: (id: string, confirmedScriptHash?: string) =>
    startReq(`/api/apps/${id}/restart`, confirmedScriptHash),
  openURL: (id: string, url?: string) =>
    req<{ opened: string }>(`/api/apps/${id}/open-url`, { method: 'POST', body: JSON.stringify({ url }) }),
  openDir: (id: string) => req<{ opened: string }>(`/api/apps/${id}/open-dir`, { method: 'POST' }),

  // Git 版本发布
  cloudBuildAlerts: () => req<import('@/types').CloudBuildStatus[]>('/api/cloud-builds'),
  acknowledgeCloudBuild: (runId: string, alertKey: string) =>
    req<{ acknowledged: boolean }>('/api/cloud-builds', { method: 'POST', body: JSON.stringify({ runId, alertKey }) }),
  releasePreflight: (id: string, checkRemote = false) =>
    req<ReleasePreflight>(`/api/apps/${id}/release/preflight?remote=${checkRemote}`, { method: 'POST' }),
  previewReleaseFile: (id: string, path: string, signal?: AbortSignal) =>
    req<import('@/types').ReleaseFilePreview>(`/api/apps/${encodeURIComponent(id)}/release/file-preview?path=${encodeURIComponent(path)}`, { signal }),
  ignoreReleaseFile: (id: string, path: string, contentFingerprint: string) =>
    req<{ignoreFile:string;preflight:ReleasePreflight}>('/api/apps/'+encodeURIComponent(id)+'/release/ignore-file', {method:'POST',body:JSON.stringify({path,contentFingerprint})}),
  releaseFindingContext: (id:string,candidateId:string,fingerprint:string,expanded:boolean,signal?:AbortSignal) =>
    req<import('@/types').ReleaseFindingContext>(`/api/apps/${encodeURIComponent(id)}/release/finding-context?${new URLSearchParams({candidateId,fingerprint,expanded:String(expanded)})}`,{signal}),
  prepareReleaseCandidate: (id: string, body: ReleaseCandidateRequest, signal?:AbortSignal) => req<ReleaseCandidate>(`/api/apps/${id}/release/candidate`, {method:'POST', body:JSON.stringify(body),signal}).then(normalizeReleaseCandidate),
  checkReleaseCandidate: (id: string, candidateId: string) => req<ReleaseCandidate>(`/api/apps/${id}/release/candidate/check`, {method:'POST', body:JSON.stringify({candidateId})}).then(normalizeReleaseCandidate),
  cancelReleaseCandidate: (id: string, candidateId: string) => req<ReleaseCandidate>(`/api/apps/${id}/release/candidate/cancel`, {method:'POST', body:JSON.stringify({candidateId})}).then(normalizeReleaseCandidate),
  getReleaseCandidate: (id: string, candidateId: string) => req<ReleaseCandidate>(`/api/apps/${id}/release/candidate/${candidateId}`).then(normalizeReleaseCandidate),
  unstageReleaseFiles: (id: string, statusFingerprint: string) =>
    req<ReleasePreflight>(`/api/apps/${id}/release/unstage`, { method: 'POST', body: JSON.stringify({ statusFingerprint }) }),
  createReleaseNotesDraft: (id: string, body: ReleaseNotesDraftRequest) =>
    req<ReleaseNotesDraft>(`/api/apps/${id}/release/notes-draft`, { method: 'POST', body: JSON.stringify(body) }),
  getReleaseProfile: (id: string) => req<ReleaseProfile>(`/api/apps/${id}/release-profile`),
  saveReleaseProfile: async (id: string, body: Omit<ReleaseProfile, 'appId' | 'updatedAt'>) => {
    const saved = await req<ReleaseProfile>(`/api/apps/${id}/release-profile`, { method: 'PATCH', body: JSON.stringify(body) })
    // Older backends accept unknown fields without persisting them. A successful
    // HTTP response is not proof that the per-project synchronization policy saved.
    if (body.syncPolicy && saved.syncPolicy !== body.syncPolicy)
      throw new ApiError(tr('同步配置尚未保存，请重启更新后的后端再试。'), 'sync_policy_not_saved')
    return saved
  },
  getReleaseConfig: (id: string) => req<ReleaseConfig>(`/api/apps/${id}/release-config`),
  getReleaseConfigFile: (id: string) =>
    req<{ path: string; content: string; exists: boolean; revision: string; example: string }>(`/api/apps/${id}/release-config/file`),
  saveReleaseConfigFile: (id: string, content: string, revision: string) =>
    req<ReleaseConfig>(`/api/apps/${id}/release-config/file`, { method: 'PUT', body: JSON.stringify({ content, revision }) }),
  scanReleaseConfig: (id: string) =>
    req<ReleaseConfig>(`/api/apps/${id}/release-config/scan`, { method: 'POST' }),
  saveReleaseConfig: (id: string, body: ReleaseConfig) =>
    req<ReleaseConfig>(`/api/apps/${id}/release-config`, { method: 'PUT', body: JSON.stringify(body) }),
  listReleases: (id: string, limit = 10) => req<ReleaseRun[]>(`/api/apps/${id}/releases?limit=${limit}`),
  createRelease: (id: string, body: CreateReleaseBody) =>
    req<ReleaseRun>(`/api/apps/${id}/releases`, { method: 'POST', body: JSON.stringify(body) }),
  getReleaseRun: (runId: string, sinceLogId = 0) =>
    req<ReleaseRunView>(`/api/releases/${runId}?sinceLogId=${sinceLogId}`),
  retryRelease: (runId: string, externalActionsConfirmed = false) =>
    req<ReleaseRun>(`/api/releases/${runId}/retry`, { method: 'POST', body: JSON.stringify({ externalActionsConfirmed }) }),
  cancelRelease: (runId: string) => req<ReleaseRunView>(`/api/releases/${runId}/cancel`, { method: 'POST' }),
  checkReleaseSync: (runId: string) => req<ReleaseRunView>(`/api/releases/${runId}/sync`, { method: 'POST' }),

  // 日志/端口
  logs: (id: string, opts?: { since?: number; limit?: number; keyword?: string }) => {
    const p = new URLSearchParams()
    if (opts?.since != null) p.set('since', String(opts.since))
    if (opts?.limit != null) p.set('limit', String(opts.limit))
    if (opts?.keyword) p.set('keyword', opts.keyword)
    return req<LogsResponse>(`/api/apps/${id}/logs?${p.toString()}`)
  },
  ports: (id: string) => req<PortEntry[]>(`/api/apps/${id}/ports`),

  // 服务角色
  setServiceRole: (appId: string, serviceId: string, role: ServiceRole) =>
    req<{ role: string; roleSource: string }>(`/api/apps/${appId}/services/${serviceId}/role`, {
      method: 'PATCH',
      body: JSON.stringify({ role }),
    }),
  reidentifyService: (appId: string, serviceId: string) =>
    req<{ role: string; roleSource: string }>(`/api/apps/${appId}/services/${serviceId}/reidentify`, {
      method: 'POST',
    }),

  // 分组
  listGroups: () => req<Group[]>('/api/groups'),
  createGroup: (body: { name: string; color?: string; order?: string[] }) =>
    req<Group>('/api/groups', { method: 'POST', body: JSON.stringify(body) }),
  updateGroup: (id: string, body: Partial<Group>) =>
    req<Group>(`/api/groups/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteGroup: (id: string) => req<{ deleted: boolean }>(`/api/groups/${id}`, { method: 'DELETE' }),

  // 设置
  getSettings: () => req<Record<string, string>>('/api/settings'),
  setSettings: (body: Record<string, string>) =>
    req<{ updated: boolean }>('/api/settings', { method: 'PATCH', body: JSON.stringify(body) }),

  // 导入导出
  exportConfig: () => req<ExportSnapshot>('/api/export'),
  importConfig: (snapshot: Partial<ExportSnapshot>) =>
    req<{ apps: number; groups: number }>('/api/import-config', {
      method: 'POST',
      body: JSON.stringify(snapshot),
    }),
}
