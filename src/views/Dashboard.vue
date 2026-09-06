<script setup lang="ts">
import { tr } from '@/i18n'

import { onBeforeUnmount, ref } from 'vue'
import AppCard from '@/components/AppCard.vue'
import UiIcon from '@/components/UiIcon.vue'
import type { AppView, Group, ServiceRole } from '@/types'

const props = defineProps<{
  apps: AppView[]
  loading: boolean
  ready: boolean
  connectionError?: string
  loadError?: string
  groups: Group[]
  moving: Record<string, boolean>
  groupView?: boolean
  nativeDrop?: boolean
}>()

const emit = defineEmits<{
  (e: 'show-import'): void
  (e: 'retry'): void
  (e: 'start', id: string): void
  (e: 'stop', id: string): void
  (e: 'restart', id: string): void
  (e: 'log', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'open-dir', id: string): void
  (e: 'release', id: string): void
  (e: 'delete', id: string): void
  (e: 'import', path: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
  (e: 'set-color', id: string, color: string): void
  (e: 'reorder', order: string[]): void
  (e: 'move-group', id: string, groupId: string): void
  (e: 'group-hover', groupId: string | null): void
}>()

const draggingId = ref<string | null>(null)
const dragOverId = ref<string | null>(null)

function onCardDragStart(event: PointerEvent, id: string) {
  if (event.button !== 0) return
  draggingId.value = id
  dragOverId.value = null
  document.body.classList.add('card-reordering')
  window.addEventListener('pointermove', onCardPointerMove, { passive: false })
  window.addEventListener('pointerup', onCardPointerUp)
  window.addEventListener('pointercancel', resetCardDrag)
  window.addEventListener('blur', resetCardDrag)
  window.addEventListener('keydown', cancelOnEscape)
}

function cancelOnEscape(event: KeyboardEvent) { if (event.key === 'Escape') resetCardDrag() }
function groupAtPoint(x: number, y: number) {
  return document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-drop-group-id]') || null
}

function cardIdAtPoint(x: number, y: number) {
  const slot = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-card-id]')
  return slot?.dataset.cardId || null
}

function onCardPointerMove(event: PointerEvent) {
  if (!draggingId.value) return
  event.preventDefault()
  const group = groupAtPoint(event.clientX, event.clientY)
  emit('group-hover', group ? group.dataset.dropGroupId! : null)
  const targetId = cardIdAtPoint(event.clientX, event.clientY)
  dragOverId.value = targetId && targetId !== draggingId.value ? targetId : null
}

function onCardPointerUp(event: PointerEvent) {
  const sourceId = draggingId.value
  const group = groupAtPoint(event.clientX, event.clientY)
  if (sourceId && group) {
    emit('move-group', sourceId, group.dataset.dropGroupId!)
    resetCardDrag()
    return
  }
  const targetId = cardIdAtPoint(event.clientX, event.clientY)
  if (!sourceId || !targetId || sourceId === targetId) {
    resetCardDrag()
    return
  }
  const order = props.apps.map((a) => a.id)
  const from = order.indexOf(sourceId)
  const to = order.indexOf(targetId)
  if (from < 0 || to < 0) {
    resetCardDrag()
    return
  }
  const [moved] = order.splice(from, 1)
  const targetAfterRemoval = order.indexOf(targetId)
  const insertAt = from < to ? targetAfterRemoval + 1 : targetAfterRemoval
  order.splice(insertAt, 0, moved)
  emit('reorder', order)
  resetCardDrag()
}

function resetCardDrag() {
  draggingId.value = null
  dragOverId.value = null
  emit('group-hover', null)
  document.body.classList.remove('card-reordering')
  window.removeEventListener('pointermove', onCardPointerMove)
  window.removeEventListener('pointerup', onCardPointerUp)
  window.removeEventListener('pointercancel', resetCardDrag)
  window.removeEventListener('blur', resetCardDrag)
  window.removeEventListener('keydown', cancelOnEscape)
}

onBeforeUnmount(resetCardDrag)
</script>

<template>
  <div class="dashboard">
    <div v-if="!ready" class="empty" role="status">
      <div class="empty-symbol" :class="{ spin: !connectionError }"><UiIcon name="refresh" :size="26" /></div>
      <h2>{{ connectionError ? tr('后台服务连接失败') : tr('正在连接后台服务…') }}</h2>
      <p>{{ connectionError || tr('连接完成后，项目会自动显示。') }}</p>
    </div>
    <div v-else-if="loading && !apps.length" class="empty" role="status">
      <div class="empty-symbol spin"><UiIcon name="refresh" :size="26" /></div>
      <h2>{{ tr('正在加载项目…') }}</h2>
    </div>
    <div v-else-if="!apps.length && loadError" class="empty" role="alert">
      <div class="empty-symbol"><UiIcon name="refresh" :size="26" /></div>
      <h2>{{ tr('项目加载失败') }}</h2>
      <p>{{ loadError }}</p>
      <button @click="emit('retry')">{{ tr('重新加载') }}</button>
    </div>
    <div v-else-if="!apps.length && groupView" class="empty">
      <div class="empty-symbol"><UiIcon name="folder" :size="26" /></div>
      <h2>{{ tr('分组暂无项目') }}</h2>
      <p>{{ tr('点击“添加项目”，或从全部应用拖入此分组。') }}</p>
    </div>
    <div v-else-if="!apps.length" class="empty welcome">
      <div class="empty-symbol"><UiIcon name="upload" :size="28" /></div>
      <h2>{{ tr('把第一个项目放进启动坞') }}</h2>
      <p>{{ nativeDrop ? tr('拖入启动脚本，确认后即可在这里启停项目、打开服务和查看日志。') : tr('粘贴启动脚本的完整路径，确认后即可在这里启停项目、打开服务和查看日志。') }}</p>
      <button class="primary" @click="emit('show-import')"><UiIcon name="plus" />{{ tr('导入脚本') }}</button>
      <span class="supported-formats">.bat <span>·</span> .cmd <span>·</span> .ps1</span>
    </div>
    <!-- 卡片网格 -->
    <div v-else class="grid">
      <div
        v-for="a in apps"
        :key="a.id"
        class="card-slot"
        :data-card-id="a.id"
        :class="{ dragging: draggingId === a.id, 'drag-over': dragOverId === a.id }"
      >
        <AppCard
          :app="a"
          :groups="groups"
          :moving="moving[a.id]"
          @move-group="(id, groupId) => emit('move-group', id, groupId)"
          @start="emit('start', $event)"
          @stop="emit('stop', $event)"
          @restart="emit('restart', $event)"
          @log="emit('log', $event)"
          @open-url="(id, url) => emit('open-url', id, url)"
          @open-dir="emit('open-dir', $event)"
          @release="emit('release', $event)"
          @delete="emit('delete', $event)"
          @rename="(id, name) => emit('rename', id, name)"
          @set-role="(appId, serviceId, role) => emit('set-role', appId, serviceId, role)"
          @reidentify="(appId, serviceId) => emit('reidentify', appId, serviceId)"
          @set-color="(id, color) => emit('set-color', id, color)"
          @drag-start="onCardDragStart"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  width: 100%;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(340px, 100%), 1fr));
  align-items: stretch;
  gap: 16px;
}
.card-slot {
  min-width: 0;
  transition: transform 0.15s ease, opacity 0.15s ease;
}
.card-slot :deep(.card) { height: 100%; }
.card-slot.dragging {
  opacity: 0.42;
}
.card-slot.drag-over {
  transform: translateY(-4px);
  outline: 2px dashed var(--accent);
  outline-offset: 4px;
  border-radius: var(--radius);
}
:global(body.card-reordering),
:global(body.card-reordering *) {
  cursor: grabbing !important;
  user-select: none !important;
}
.empty { min-height: 340px; display: flex; flex-direction: column; align-items: center; justify-content: center; text-align: center; padding: 64px 24px; color: var(--text-dim); }
.empty-symbol { color: var(--text-dim); margin-bottom: 20px; }
.empty h2 { margin: 0 0 10px; font-size: 20px; font-weight: 600; color: var(--text); }
.empty p { max-width: 440px; line-height: 1.8; margin: 0; overflow-wrap: anywhere; }
.empty button { margin-top: 24px; display: inline-flex; align-items: center; gap: 8px; }
.supported-formats { margin-top: 16px; color: var(--text-faint); font: 12px Consolas, monospace; }
.supported-formats span { margin: 0 9px; }
.spin { animation: spin 1.4s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
