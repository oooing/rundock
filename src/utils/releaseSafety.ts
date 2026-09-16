import type { ReleaseFileClassification, ReleaseManualDecision } from '../types'

// Preserve explicit decisions only while the exact file content is unchanged.
export function reconcileReleaseSelection(files: ReleaseFileClassification[], decisions: ReleaseManualDecision[]) {
  const current = new Map(files.map(file => [file.path, file]))
  const retained = decisions.filter(decision => current.get(decision.path)?.contentFingerprint === decision.contentFingerprint)
  const choices = new Map(retained.map(decision => [decision.path, decision.decision === 'include']))
  const selected: Record<string, boolean> = {}
  for (const file of files) selected[file.path] = file.category !== 'sensitive' && (choices.get(file.path) ?? file.selectedDefault)
  return { selected, decisions: retained }
}

export function recommendedReleaseSelection(files: ReleaseFileClassification[]) {
  return Object.fromEntries(files.map(file => [file.path, file.category === 'recommend' && file.selectedDefault]))
}
