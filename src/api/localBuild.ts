import { getBaseURL } from './base'
import { tr } from '@/i18n'
import type { BuildArtifactMode, LocalBuildArtifact, LocalBuildList, LocalBuildRequest, LocalBuildRun, LocalBuildView, SavedBuildArtifacts } from '@/types/localBuild'

export class LocalBuildError extends Error {
  constructor(message: string, public status: number, public code = '') { super(message) }
}

async function checkedResponse(path: string, init: RequestInit): Promise<Response> {
  const response = await fetch(`${getBaseURL()}${path}`, { ...init, headers: { 'Content-Type': 'application/json', ...init.headers } })
  if (response.ok) return response
  let message = `HTTP ${response.status}`
  let code = ''
  try {
    const body = await response.json()
    if (typeof body.error === 'string') message = body.error
    if (typeof body.code === 'string') code = body.code
  } catch { /* An HTTP error without JSON still needs a readable status. */ }
  throw new LocalBuildError(tr(message), response.status, code)
}
async function json<T>(path: string, init: RequestInit = {}): Promise<T> {
  return (await checkedResponse(path, init)).json() as Promise<T>
}
function artifactBase(runId: string, mode: BuildArtifactMode) {
  return `/api/${mode === 'release' ? 'releases' : 'local-builds'}/${encodeURIComponent(runId)}`
}

export const localBuildApi = {
  list: (appId: string, signal?: AbortSignal) => json<LocalBuildList>(`/api/apps/${encodeURIComponent(appId)}/local-builds`, { signal }),
  create: (appId: string, body: LocalBuildRequest, signal?: AbortSignal) => json<LocalBuildRun>(`/api/apps/${encodeURIComponent(appId)}/local-builds`, { method: 'POST', body: JSON.stringify(body), signal }),
  view: (runId: string, sinceLogId = 0, signal?: AbortSignal) => json<LocalBuildView>(`/api/local-builds/${encodeURIComponent(runId)}?sinceLogId=${sinceLogId}`, { signal }),
  cancel: (runId: string, signal?: AbortSignal) => json<unknown>(`/api/local-builds/${encodeURIComponent(runId)}/cancel`, { method: 'POST', signal }),
  savedReleaseArtifacts: (runId: string, signal?: AbortSignal) => json<SavedBuildArtifacts>(`${artifactBase(runId, 'release')}/saved-artifacts`, { signal }),
  openDirectory: (runId: string, signal?: AbortSignal, mode: BuildArtifactMode = 'local') => json<unknown>(`${artifactBase(runId, mode)}/${mode === 'release' ? 'open-artifact-dir' : 'open-dir'}`, { method: 'POST', signal }),
  download: async (runId: string, artifact: LocalBuildArtifact, signal?: AbortSignal, mode: BuildArtifactMode = 'local'): Promise<Blob> => {
    const response = await checkedResponse(`${artifactBase(runId, mode)}/artifacts/${encodeURIComponent(artifact.id)}`, { signal })
    const blob = await response.blob()
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    if (blob.size !== artifact.sizeBytes) throw new Error(tr('下载文件大小不一致，请重试。'))
    if (!/^[a-f\d]{64}$/i.test(artifact.sha256)) throw new Error(tr('产物校验信息无效，请重新读取任务。'))
    const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer())
    const actual = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
    if (actual !== artifact.sha256.toLowerCase()) throw new Error(tr('下载文件校验失败，请重试。'))
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    return blob
  },
}
