export type DropEdge = 'before' | 'after'
export function reorderAtEdge(order: string[], source: string, target: string, edge: DropEdge): string[] {
  if (source === target || !order.includes(source) || !order.includes(target)) return order
  const next = order.filter(id => id !== source)
  next.splice(next.indexOf(target) + (edge === 'after' ? 1 : 0), 0, source)
  return next
}
export function edgeAtPoint(rect: { left: number; top: number; width: number; height: number }, x: number, y: number, singleColumn: boolean): DropEdge {
  return (singleColumn ? y < rect.top + rect.height / 2 : x < rect.left + rect.width / 2) ? 'before' : 'after'
}
