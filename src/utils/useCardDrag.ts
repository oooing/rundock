import { onBeforeUnmount, ref } from 'vue'
import { tr } from '@/i18n'
import { edgeAtPoint, reorderAtEdge, type DropEdge } from './cardDrag'

type Item = { id: string; name: string }
export function useCardDrag(options: {
  items: () => Item[]
  reorder: (ids: string[]) => void
  moveGroup: (id: string, groupId: string) => void
  hoverGroup: (groupId: string | null) => void
}) {
  const draggingId = ref<string | null>(null)
  const dragOverId = ref<string | null>(null)
  const dropEdge = ref<DropEdge>('before')
  const singleColumn = ref(false)
  const dragMessage = ref('')
  let pending: { id: string; pointerId: number; startX: number; startY: number; x: number; y: number; handle: HTMLElement; slot: HTMLElement } | null = null
  let preview: HTMLElement | null = null
  let scrollArea: HTMLElement | null = null
  let frame = 0

  function resetCardDrag() {
    const handle = pending?.handle, pointerId = pending?.pointerId
    pending = null
    if (frame) cancelAnimationFrame(frame)
    frame = 0
    preview?.remove()
    preview = null
    draggingId.value = dragOverId.value = null
    dragMessage.value = ''
    options.hoverGroup(null)
    document.body.classList.remove('card-reordering')
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', onUp)
    window.removeEventListener('pointercancel', resetCardDrag)
    window.removeEventListener('blur', resetCardDrag)
    window.removeEventListener('keydown', onEscape)
    if (pointerId !== undefined && handle?.hasPointerCapture(pointerId)) handle.releasePointerCapture(pointerId)
  }
  function onEscape(e: KeyboardEvent) {
    if (e.key !== 'Escape') return
    e.preventDefault()
    resetCardDrag()
  }
  function groupAt(x: number, y: number) {
    return document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-drop-group-id]') || null
  }
  function updateTarget() {
    if (!pending || !draggingId.value) return
    const { x, y, slot } = pending
    const group = groupAt(x, y)
    options.hoverGroup(group ? group.dataset.dropGroupId! : null)
    dragOverId.value = null
    if (group) {
      dragMessage.value = tr('松开以移到「{0}」', [group.querySelector('.label')?.textContent || ''])
      return
    }
    const grid = slot.parentElement!
    const bounds = grid.getBoundingClientRect()
    const area = scrollArea?.getBoundingClientRect()
    if (x < bounds.left || x > bounds.right || y < bounds.top || y > bounds.bottom || (area && (y < area.top || y > area.bottom))) {
      dragMessage.value = tr('拖到卡片之间排序，或拖到侧栏分组')
      return
    }
    const slots = [...grid.querySelectorAll<HTMLElement>('[data-card-id]')]
    // Nearest rectangle also accepts the gap between cards.
    const target = slots.map(el => {
      const r = el.getBoundingClientRect()
      const dx = Math.max(r.left - x, 0, x - r.right), dy = Math.max(r.top - y, 0, y - r.bottom)
      return { el, r, distance: dx * dx + dy * dy }
    }).sort((a, b) => a.distance - b.distance)[0]
    if (!target || target.el.dataset.cardId === pending.id) {
      dragMessage.value = tr('拖到卡片之间排序，或拖到侧栏分组')
      return
    }
    singleColumn.value = grid.clientWidth < target.r.width * 1.5
    dropEdge.value = edgeAtPoint(target.r, x, y, singleColumn.value)
    dragOverId.value = target.el.dataset.cardId!
    const name = options.items().find(item => item.id === dragOverId.value)?.name || ''
    dragMessage.value = tr(dropEdge.value === 'before' ? '放到「{0}」前面' : '放到「{0}」后面', [name])
  }
  function tick() {
    if (!pending || !preview) return
    preview.style.transform = `translate3d(${pending.x - pending.startX}px, ${pending.y - pending.startY}px, 0) scale(1.025)`
    if (scrollArea) {
      const r = scrollArea.getBoundingClientRect()
      if (pending.x >= r.left && pending.x <= r.right && pending.y >= r.top && pending.y <= r.bottom) {
        const edge = 52
        const speed = pending.y < r.top + edge ? -(1 - (pending.y - r.top) / edge) : pending.y > r.bottom - edge ? 1 - (r.bottom - pending.y) / edge : 0
        scrollArea.scrollTop += speed * 12
      }
    }
    updateTarget()
    frame = requestAnimationFrame(tick)
  }
  function activate() {
    if (!pending) return
    const card = pending.slot.querySelector<HTMLElement>('article')!
    const rect = card.getBoundingClientRect()
    preview = card.cloneNode(true) as HTMLElement
    preview.classList.add('card-drag-preview')
    preview.dataset.motion = 'none'
    preview.setAttribute('aria-hidden', 'true')
    preview.inert = true
    preview.removeAttribute('id')
    preview.querySelectorAll('[id]').forEach(el => el.removeAttribute('id'))
    preview.querySelectorAll('details[open]').forEach(el => el.removeAttribute('open'))
    preview.querySelectorAll('.role-menu').forEach(el => el.remove())
    Object.assign(preview.style, { left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px` })
    document.body.append(preview)
    draggingId.value = pending.id
    document.body.classList.add('card-reordering')
    tick()
  }
  function onMove(e: PointerEvent) {
    if (!pending || e.pointerId !== pending.pointerId) return
    e.preventDefault()
    pending.x = e.clientX; pending.y = e.clientY
    if (!draggingId.value && Math.hypot(pending.x - pending.startX, pending.y - pending.startY) >= 6) activate()
  }
  function onUp(e: PointerEvent) {
    if (!pending || e.pointerId !== pending.pointerId) return
    const source = pending.id
    pending.x = e.clientX; pending.y = e.clientY
    if (draggingId.value) {
      updateTarget()
      const group = groupAt(e.clientX, e.clientY)
      if (group) options.moveGroup(source, group.dataset.dropGroupId!)
      else if (dragOverId.value) {
        const current = options.items().map(item => item.id)
        const next = reorderAtEdge(current, source, dragOverId.value, dropEdge.value)
        if (next.some((id, index) => current[index] !== id)) options.reorder(next)
      }
    }
    resetCardDrag()
  }
  function onCardDragStart(e: PointerEvent, id: string) {
    if (e.button !== 0 || pending) return
    const handle = e.currentTarget as HTMLElement
    const slot = handle.closest<HTMLElement>('[data-card-id]')
    if (!slot) return
    handle.focus()
    pending = { id, pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, x: e.clientX, y: e.clientY, handle, slot }
    scrollArea = slot.closest<HTMLElement>('.content')
    handle.setPointerCapture(e.pointerId)
    window.addEventListener('pointermove', onMove, { passive: false })
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', resetCardDrag)
    window.addEventListener('blur', resetCardDrag)
    window.addEventListener('keydown', onEscape)
  }
  function onKeyboardReorder(id: string, direction: number) {
    const order = options.items().map(item => item.id), from = order.indexOf(id)
    const target = order[from + direction]
    if (from < 0 || !target) return
    options.reorder(reorderAtEdge(order, id, target, direction < 0 ? 'before' : 'after'))
  }
  onBeforeUnmount(resetCardDrag)
  return { draggingId, dragOverId, dropEdge, singleColumn, dragMessage, onCardDragStart, onKeyboardReorder }
}
