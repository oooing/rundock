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
  ServiceRole,
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
  deleteApp: (id: string) => req<{ deleted: boolean }>(`/api/apps/${id}`, { method: 'DELETE' }),

  // 操作
  start: (id: string) => req<{ started: boolean }>(`/api/apps/${id}/start`, { method: 'POST' }),
  stop: (id: string) => req<{ stopped: boolean }>(`/api/apps/${id}/stop`, { method: 'POST' }),
  restart: (id: string) => req<{ restarted: boolean }>(`/api/apps/${id}/restart`, { method: 'POST' }),
  openURL: (id: string, url?: string) =>
    req<{ opened: string }>(`/api/apps/${id}/open-url`, { method: 'POST', body: JSON.stringify({ url }) }),
  openDir: (id: string) => req<{ opened: string }>(`/api/apps/${id}/open-dir`, { method: 'POST' }),

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
