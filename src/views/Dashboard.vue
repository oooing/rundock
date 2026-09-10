<script setup lang="ts">
import { tr } from '@/i18n'

import { useCardDrag } from '@/utils/useCardDrag'
import AppCard from '@/components/AppCard.vue'
import UiIcon from '@/components/UiIcon.vue'
import type { AppView, CloudBuildStatus, Group, ServiceRole } from '@/types'

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
  cloudAlerts?: CloudBuildStatus[]
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
  (e: 'cloud-details', id: string): void
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

const { draggingId, dragOverId, dropEdge, singleColumn, dragMessage, onCardDragStart, onKeyboardReorder } = useCardDrag({
  items: () => props.apps,
  reorder: order => emit('reorder', order),
  moveGroup: (id, groupId) => emit('move-group', id, groupId),
  hoverGroup: groupId => emit('group-hover', groupId),
})

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
      <p>{{ tr('选择项目文件夹或启动脚本，添加后即可一键启停。') }}</p>
      <button class="primary" @click="emit('show-import')"><UiIcon name="plus" />{{ tr('添加项目') }}</button>
      <span class="supported-formats">.bat <span>·</span> .cmd <span>·</span> .ps1</span>
    </div>
    <!-- 卡片网格 -->
    <div v-else class="grid">
      <div
        v-for="a in apps"
        :key="a.id"
        class="card-slot"
        :data-card-id="a.id"
        :class="{ dragging: draggingId === a.id, 'drag-over': dragOverId === a.id, 'drop-before': dragOverId === a.id && dropEdge === 'before', 'drop-after': dragOverId === a.id && dropEdge === 'after', 'drop-horizontal': singleColumn }"
      >
        <AppCard
          :app="a"
          :cloud-alerts="cloudAlerts?.filter(alert => alert.appId === a.id)"
          @cloud-details="emit('cloud-details', $event)"
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
          @reorder-key="onKeyboardReorder"
        />
      </div>
    </div>
    <div v-if="draggingId" class="drag-status" role="status">{{ dragMessage }}<kbd>Esc</kbd>{{ tr('取消') }}</div>
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
  position: relative;
  min-width: 0;
  transition: transform 0.15s ease, opacity 0.15s ease;
}
.card-slot :deep(.card) { height: 100%; }
.card-slot.dragging {
  outline: 2px dashed var(--accent);
  outline-offset: -2px;
  border-radius: var(--radius);
}
.card-slot.dragging :deep(.card) { opacity: 0.22; }
.card-slot.dragging :deep(.card::before), .card-slot.dragging :deep(.card::after) { display: none; }
.card-slot.drag-over::before { content: ''; position: absolute; top: 0; bottom: 0; width: 3px; background: var(--accent); border-radius: 3px; z-index: 5; pointer-events: none; box-shadow: 0 0 0 1px var(--bg); }
.card-slot.drop-before::before { left: -10px; }
.card-slot.drop-after::before { right: -10px; }
.card-slot.drag-over::after { content: ''; position: absolute; top: -4px; width: 9px; height: 9px; border: 2px solid var(--accent); background: var(--bg); border-radius: 50%; z-index: 5; pointer-events: none; }
.card-slot.drop-before::after { left: -13px; }
.card-slot.drop-after::after { right: -13px; }
.card-slot.drop-horizontal.drag-over::before { left: 0; right: 0; width: auto; height: 3px; bottom: auto; }
.card-slot.drop-horizontal.drop-before::before { top: -10px; }
.card-slot.drop-horizontal.drop-after::before { top: auto; bottom: -10px; }
.card-slot.drop-horizontal.drag-over::after { left: -4px; right: auto; }
.card-slot.drop-horizontal.drop-before::after { top: -13px; }
.card-slot.drop-horizontal.drop-after::after { top: auto; bottom: -13px; }
:global(.card-drag-preview) { position: fixed !important; margin: 0 !important; z-index: 200; pointer-events: none !important; transform-origin: top left; transition: none !important; box-shadow: 0 20px 50px rgba(0,0,0,.5), 0 0 0 1px var(--card-fg, var(--text)); opacity: 0.97; }
:global(.card-drag-preview *) { pointer-events: none !important; }
.drag-status { position: fixed; bottom: 26px; left: 50%; transform: translateX(-50%); z-index: 210; pointer-events: none; display: flex; align-items: center; gap: 8px; max-width: calc(100vw - 32px); padding: 10px 16px; border: 1px solid var(--border); border-radius: 9px; color: var(--text); background: var(--bg-elev); box-shadow: var(--shadow); font-size: 13px; }
.drag-status kbd { margin-left: 12px; padding: 2px 4px; border: 1px solid var(--border); border-radius: 4px; font-size: 11px; }
@media (prefers-reduced-motion: reduce) { .card-slot { transition: none; } }
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
