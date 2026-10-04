import type { ReleaseLog, ReleaseRun } from './index'
import type { ReleaseTargetRun } from './releaseExecution'

export interface LocalBuildTarget {
  id: string
  name: string
  kind: string
  currentVersion: string
  check?: boolean
  build: boolean
  package: boolean
  available: boolean
  reason?: string
}

export interface LocalBuildPreparation {
  projectRoot: string
  configFingerprint: string
  targets: LocalBuildTarget[]
  help?: string
}

export type LocalBuildRun = Omit<ReleaseRun, 'status' | 'stage'> & {
  intent?: 'build-only'
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  stage: string
}

export interface LocalBuildRequest {
  requestId: string
  configFingerprint: string
  targetIds: string[]
}

export interface LocalBuildArtifact {
  id: string
  targetId: string
  name: string
  sizeBytes: number
  sha256: string
  available: boolean
  error?: string
}

export interface LocalBuildView {
  run: LocalBuildRun
  targets: Array<Omit<ReleaseTargetRun, 'status'> & { status: string }>
  logs: ReleaseLog[]
  artifacts: LocalBuildArtifact[]
  outputDirectory: string
}

export interface SavedBuildArtifacts {
  artifacts: LocalBuildArtifact[]
  outputDirectory: string
}

export type BuildArtifactMode = 'local' | 'release'

export interface LocalBuildList {
  preparation: LocalBuildPreparation | null
  preparationError?: string
  recentRuns: LocalBuildRun[]
}
