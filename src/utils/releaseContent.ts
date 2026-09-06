export function releaseContentState(
  createTag: boolean,
  selectedPaths: string[],
  changedPaths: string[],
  baseTags: string[],
  commitsSinceTags?: Record<string, number>,
): 'new' | 'none' | 'unknown' {
  // Preserve the existing no-Tag build / push workflow.
  if (!createTag || selectedPaths.some(path => changedPaths.includes(path))) return 'new'
  const counts = baseTags.map(tag => commitsSinceTags?.[tag])
  if (counts.some(count => typeof count === 'number' && count > 0)) return 'new'
  if (!counts.length || counts.some(count => count === undefined)) return 'unknown'
  return 'none'
}
