// Only reusable choices belong here. Never persist versions, files, checks or approvals.
export interface ReleasePreferences {
  buildMode?: 'github' | 'local'
  syncPolicy?: 'auto' | 'local'
  localSyncPolicy?: 'auto' | 'local'
  releaseIntent?: 'formal' | 'save-progress'
  localVersionMode?: 'current' | 'upgrade'
  versionMode?: 'auto' | 'manual'
  checksEnabled?: boolean
  releaseTargets?: Partial<Record<'github' | 'local', string[]>>
  packagingTargets?: string[]
}
export const releasePreferenceKey = (appId: string) => `launcher.release-preferences.${appId}`
function ids(value: unknown): string[] | undefined {
  return Array.isArray(value) && value.every(id => typeof id === 'string' && id.length > 0)
    ? [...new Set(value)] : undefined
}
export function readReleasePreferences(appId: string): ReleasePreferences {
  try {
    const raw = JSON.parse(localStorage.getItem(releasePreferenceKey(appId)) || '{}')
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
    const result: ReleasePreferences = {}
    if (['github', 'local'].includes(raw.buildMode)) result.buildMode = raw.buildMode
    if (['auto', 'local'].includes(raw.syncPolicy)) result.syncPolicy = raw.syncPolicy
    if (['auto', 'local'].includes(raw.localSyncPolicy)) result.localSyncPolicy = raw.localSyncPolicy
    if (['formal', 'save-progress'].includes(raw.releaseIntent)) result.releaseIntent = raw.releaseIntent
    if (['current', 'upgrade'].includes(raw.localVersionMode)) result.localVersionMode = raw.localVersionMode
    if (['auto', 'manual'].includes(raw.versionMode)) result.versionMode = raw.versionMode
    if (typeof raw.checksEnabled === 'boolean') result.checksEnabled = raw.checksEnabled
    for (const mode of ['github', 'local'] as const) {
      const selected = ids(raw.releaseTargets?.[mode])
      if (selected) (result.releaseTargets ??= {})[mode] = selected
    }
    const packaging = ids(raw.packagingTargets)
    if (packaging) result.packagingTargets = packaging
    return result
  } catch { return {} }
}
export function writeReleasePreferences(appId: string, patch: ReleasePreferences) {
  // Merge at write time: the packaging module and release form save independently.
  const previous = readReleasePreferences(appId)
  localStorage.setItem(releasePreferenceKey(appId), JSON.stringify({ ...previous, ...patch,
    releaseTargets: { ...previous.releaseTargets, ...patch.releaseTargets } }))
}
