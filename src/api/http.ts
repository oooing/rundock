// HTTP 客户端：所有 REST 调用集中在此，返回类型化结果。
import { getBaseURL } from './base'
import type {
  AppView,
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
  ReleaseConfig,
  ReleaseNotesDraft,
  ReleaseNotesDraftRequest,
  ReleasePreflight,
  ReleaseProfile,
  ReleaseRun,
  ReleaseRunView,
} from '@/types'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(`${getBaseURL()}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try {
      const body = await r.json()
      msg = body.error || msg
    } catch {
      /* ignore */
    }
    throw new Error(msg)
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
    const body = (await r.json()) as ScriptConfirmationResponse
    return { ok: false, confirmation: body }
  }
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try {
      const body = await r.json()
      msg = body.error || msg
    } catch {
      /* ignore */
    }
    throw new Error(msg)
  }
  const data = (await r.json()) as StartResponse
  return { ok: true, data }
}

export const api = {
  // 导入（只读分析）
  import: (scriptPath: string) =>
    req<ImportCandidate>('/api/import', {
      method: 'POST',
      body: JSON.stringify({ scriptPath }),
    }),

  // Apps
  listApps: () => req<AppView[]>('/api/apps'),
  getApp: (id: string) => req<AppView>(`/api/apps/${id}`),
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
  releasePreflight: (id: string, checkRemote = true) =>
    req<ReleasePreflight>(`/api/apps/${id}/release/preflight${checkRemote ? '' : '?remote=false'}`, { method: 'POST' }),
  createReleaseNotesDraft: (id: string, body: ReleaseNotesDraftRequest) =>
    req<ReleaseNotesDraft>(`/api/apps/${id}/release/notes-draft`, { method: 'POST', body: JSON.stringify(body) }),
  getReleaseProfile: (id: string) => req<ReleaseProfile>(`/api/apps/${id}/release-profile`),
  saveReleaseProfile: (id: string, body: Omit<ReleaseProfile, 'appId' | 'updatedAt'>) =>
    req<ReleaseProfile>(`/api/apps/${id}/release-profile`, { method: 'PATCH', body: JSON.stringify(body) }),
  getReleaseConfig: (id: string) => req<ReleaseConfig>(`/api/apps/${id}/release-config`),
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
