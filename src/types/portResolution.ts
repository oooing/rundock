export type PortResolutionState = 'clear' | 'running' | 'conflict' | 'reserved' | 'unknown'

export interface PortConflict {
  port: number
  pid: number
  name: string
  managedAppId?: string
  managedAppName?: string
  canClose: boolean
  reason?: string
}

/** Read-only plan. The token binds explicit confirmation to these process identities. */
export interface PortResolution {
  state: PortResolutionState
  message: string
  conflicts: PortConflict[]
  reservedPorts: number[]
  canResolve: boolean
  confirmationToken?: string
  expiresAt?: string
}
