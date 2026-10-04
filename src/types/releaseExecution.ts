export interface ReleaseTargetRun {
  releaseRunId: string
  targetId: string
  build: boolean
  package: boolean
  publish: boolean
  deploy: boolean
  checkDone: boolean
  buildDone: boolean
  packageDone: boolean
  publishDone: boolean
  deployDone: boolean
  status: 'waiting' | 'queued' | 'running' | 'triggered' | 'remote_pending' | 'handed_off' | 'succeeded' | 'failed'
  stage: string
  errorCode: string
  errorMessage: string
  startedAt: string | null
  finishedAt: string | null
}

export interface ReleaseArtifact {
  id: number
  releaseRunId: string
  targetId: string
  path: string
  sizeBytes: number
  sha256: string
  createdAt: string
}

export interface ReleaseAutomationStatus {
  provider: string
  workflow: string
  url: string
  state: string
  message: string
}


export interface ReleaseDelivery {
  runId: string; groupId: string; manifestSha256: string; state: string
  releaseId: number; url: string; errorCode: string; errorMessage: string
  syncState: string; syncMessage: string; updatedAt: string
}
