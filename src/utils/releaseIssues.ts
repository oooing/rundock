import type { ReleaseCandidate } from '@/types'

export function groupSensitiveFindings(findings: ReleaseCandidate['sensitiveFindings']) {
  const groups = new Map<string, ReleaseCandidate['sensitiveFindings']>()
  for (const finding of findings) groups.set(finding.path, [...(groups.get(finding.path) || []), finding])
  return [...groups].map(([path, findings]) => ({ path, findings }))
}

export function hasReleaseIssues(candidate: ReleaseCandidate | null) {
  return !!candidate && (candidate.sensitiveFindings.length > 0 ||
    candidate.dependencyFindings.some(finding => finding.blocked) ||
    candidate.checkResults.some(check => check.required && ['failed', 'unverified', 'blocked'].includes(check.status)) ||
    ['failed', 'blocked'].includes(candidate.status))
}
