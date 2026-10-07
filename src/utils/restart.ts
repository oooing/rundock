export interface RestartPlan {
  kind: 'managed' | 'external' | 'self'
  canRestart: boolean
  message: string
  processes: { pid: number; name: string }[]
  confirmationToken?: string
  expiresAt?: string
}

export function restartConfirmationValid(plan: RestartPlan | null, now = Date.now()): boolean {
  return !!(plan?.canRestart && plan.confirmationToken && plan.expiresAt
    && Date.parse(plan.expiresAt) > now)
}

export function isReplacementReady(previous: string, health: { instanceId?: string; status?: string; apiVersion?: string }): boolean {
  return !!(previous && health.instanceId && health.instanceId !== previous
    && health.status === 'ok' && health.apiVersion === '2')
}
