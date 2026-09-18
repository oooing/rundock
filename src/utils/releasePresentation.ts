import type { ReleaseTarget } from '@/types'

// Runner details belong to the build-mode selector, not the product name.
export function releaseTargetLabel(name: string) {
  return name.replace(/\s*[·・]\s*(云端|本地)\s*$/, '').trim()
}

export function isAlternateBuildTarget(target: ReleaseTarget, other: ReleaseTarget) {
  return target.id !== other.id && !!target.versionGroup &&
    target.versionGroup === other.versionGroup && target.kind === other.kind &&
    releaseTargetLabel(target.name) === releaseTargetLabel(other.name) &&
    target.runner.type !== other.runner.type
}
