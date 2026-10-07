<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { AppView, CloudBuildStatus, Group, ServiceRole } from '@/types'
import { useAppsStore } from '@/stores/apps'
import UiIcon from '@/components/UiIcon.vue'
import { CARD_COLOR_PALETTE, getCardVisualStyle, normalizeHexColor } from '@/utils/cardColors'
import { runningEffect, startingEffect } from '@/stores/motion'
import StartupIndicator from './StartupIndicator.vue'
import { cardServices, cardServiceDetails } from '@/utils/cardServices'
import { useAppStartup } from '@/composables/useAppStartup'
import AppStartupNotice from './AppStartupNotice.vue'
import PortResolutionDialog from './PortResolutionDialog.vue'
import RestartDialog from './RestartDialog.vue'

const props = defineProps<{ app: AppView; groups: Group[]; moving?: boolean; cloudAlerts?: CloudBuildStatus[] }>()
const emit = defineEmits<{
  (e: 'start' | 'stop' | 'restart' | 'log' | 'open-dir' | 'release' | 'delete', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
  (e: 'set-color', id: string, color: string): void
  (e: 'drag-start', event: PointerEvent, id: string): void
  (e: 'reorder-key', id: string, direction: number): void
  (e: 'move-group', id: string, groupId: string): void
  (e: 'cloud-details', id: string): void
}>()

const a = computed(() => props.app)
const appsStore = useAppsStore()
const { runtimeLocked, startupIssue, checkingIssue, resolvingPorts, operationBusy, portIssue, statusLabel } = useAppStartup(a)
const portDialog = ref<InstanceType<typeof PortResolutionDialog> | null>(null)
const restartDialog = ref<InstanceType<typeof RestartDialog> | null>(null)
const canInspectConflict = computed(() => a.value.runtimeCheck?.state === 'conflict' || (!runtimeLocked.value && a.value.status === 'failed' && portIssue.value))
const buildFailures = computed(() => (props.cloudAlerts || []).filter(alert => alert.state === 'failed'))
const buildBadge = computed(() => buildFailures.value.length ? tr('构建失败') : tr('构建待确认'))

function chooseGroup(event: Event) {
  const select = event.target as HTMLSelectElement
  const groupId = select.value
  select.value = a.value.groupId || ''
  emit('move-group', a.value.id, groupId)
}

// 服务角色 → 图标/颜色/标签
const ROLE_META = computed<Record<ServiceRole, { icon: string; label: string }>>(() => ({
  frontend: { icon: 'globe', label: tr("前端") },
  backend: { icon: 'server', label: tr("后端") },
  database: { icon: 'database', label: tr("数据库") },
  unknown: { icon: 'help', label: tr("未识别") },
}))
const ROLE_OPTIONS: ServiceRole[] = ['frontend', 'backend', 'database', 'unknown']

// 当前展开切换菜单的 service id（null = 无）
const roleMenuOpen = ref<string | null>(null)
const roleMenuStyle = ref<Record<string, string>>({})
const cardElement = ref<HTMLElement | null>(null)
const manageDetails = ref<HTMLDetailsElement | null>(null)
const manageSummary = ref<HTMLElement | null>(null)
const nameInput = ref<HTMLInputElement | null>(null)
const nameButton = ref<HTMLButtonElement | null>(null)
let roleTrigger: HTMLButtonElement | null = null
// role 可能为 undefined（老数据/未填充），默认回退到 unknown，避免 ROLE_META[undefined] 崩溃。
function roleMeta(role?: ServiceRole) {
  return ROLE_META.value[role ?? 'unknown']
}
async function toggleRoleMenu(svcId: string, event: MouseEvent) {
  const opening = roleMenuOpen.value !== svcId
  closeMenus()
  if (!opening) return
  roleTrigger = event.currentTarget as HTMLButtonElement
  const anchor = roleTrigger.getBoundingClientRect()
  roleMenuStyle.value = { left: `${anchor.left}px`, top: `${anchor.bottom + 5}px` }
  roleMenuOpen.value = svcId
  await nextTick()
  const menu = cardElement.value?.querySelector<HTMLElement>('.role-menu')
  if (menu) {
    const bounds = menu.getBoundingClientRect()
    roleMenuStyle.value = {
      left: `${Math.max(8, Math.min(anchor.left, document.documentElement.clientWidth - bounds.width - 8))}px`,
      top: `${Math.max(8, anchor.bottom + 5 + bounds.height <= window.innerHeight - 8 ? anchor.bottom + 5 : anchor.top - bounds.height - 5)}px`,
    }
    menu.querySelector<HTMLButtonElement>('button')?.focus({ preventScroll: true })
  }
}
function pickRole(svcId: string, role: ServiceRole) {
  closeMenus(true)
  emit('set-role', a.value.id, svcId, role)
}
function reidentify(svcId: string) {
  closeMenus(true)
  emit('reidentify', a.value.id, svcId)
}

function closeMenus(restoreFocus = false) {
  const wasRoleOpen = !!roleMenuOpen.value
  const wasManageOpen = !!manageDetails.value?.open
  roleMenuOpen.value = null
  colorMenuOpen.value = false
  if (manageDetails.value) manageDetails.value.open = false
  if (restoreFocus) {
    if (wasRoleOpen) roleTrigger?.focus()
    else if (wasManageOpen) manageSummary.value?.focus()
  }
}
function onEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || (!roleMenuOpen.value && !manageDetails.value?.open)) return
  event.preventDefault()
  event.stopPropagation()
  closeMenus(true)
}
function onOutsidePointer(event: PointerEvent) {
  const target = event.target as Node
  if (roleMenuOpen.value && !cardElement.value?.querySelector('.role-menu')?.contains(target) && !roleTrigger?.contains(target)) closeMenus()
  if (manageDetails.value?.open && !manageDetails.value.contains(target)) closeMenus()
}
function onMenuFocusOut(event: FocusEvent) {
  const container = event.currentTarget as HTMLElement
  if (!event.relatedTarget || container.contains(event.relatedTarget as Node)) return
  closeMenus()
}
function onManageToggle() {
  if (manageDetails.value?.open) roleMenuOpen.value = null
  else colorMenuOpen.value = false
}
function onPageScroll(event: Event) {
  if (roleMenuOpen.value && !cardElement.value?.querySelector('.role-menu')?.contains(event.target as Node)) closeMenus()
}
function onResize() { closeMenus() }
onMounted(() => {
  document.addEventListener('pointerdown', onOutsidePointer)
  document.addEventListener('scroll', onPageScroll, true)
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutsidePointer)
  document.removeEventListener('scroll', onPageScroll, true)
  window.removeEventListener('resize', onResize)
})

// 服务列表变化时，若当前打开菜单的 service 已不存在，则关闭菜单并清除幻影遮罩。
watch(
  () => a.value.services?.map((s) => s.id),
  (ids) => {
    if (roleMenuOpen.value && ids && !ids.includes(roleMenuOpen.value)) {
      roleMenuOpen.value = null
    }
  }
)

// 重命名
const editingName = ref(false)
const nameDraft = ref('')
async function startRename() {
  closeMenus()
  nameDraft.value = a.value.name
  editingName.value = true
  await nextTick()
  nameInput.value?.focus()
  nameInput.value?.select()
}
async function commitRename(restoreFocus = false) {
  if (!editingName.value) return
  const n = nameDraft.value.trim()
  if (n && n !== a.value.name) {
    emit('rename', a.value.id, n)
  }
  editingName.value = false
  if (restoreFocus) { await nextTick(); nameButton.value?.focus() }
}
async function cancelRename() {
  nameDraft.value = a.value.name
  editingName.value = false
  await nextTick()
  nameButton.value?.focus()
}

const isActive = computed(
  () => ['starting', 'running', 'degraded', 'stopping'].includes(a.value.status)
)
// URL 仅在服务运行时才可达；停止/失败时置灰，避免点了浏览器显示无法访问造成"链接坏了"的误会。
const urlReachable = computed(() => isActive.value)

// 服务健康状态文本
function healthText(h: string): string {
  const m: Record<string, string> = {
    healthy: tr("健康"),
    unhealthy: tr("不健康"),
    unknown: tr("检测中"),
  }
  return m[h] || h
}

const sortedServices = computed(() => cardServices(a.value))
const serviceDetails = computed(() => cardServiceDetails(a.value))
const primaryURLInServices = computed(() => sortedServices.value.some(svc => svc.url === a.value.lastUrl))
const serviceState = (svc: ReturnType<typeof cardServices>[number]) => a.value.runtimeCheck?.state === 'running' && svc.source === 'current' ? tr('正在监听') : svc.source === 'current'
  ? healthText(svc.health) : svc.source === 'history' ? tr('上次发现') : tr('配置端口')

// 打开某个服务的 URL（通过 open-url 事件，后端会用系统浏览器打开）
function openServiceUrl(url: string) {
  emit('open-url', a.value.id, url)
}

// ===== 卡片背景色 =====
// 只持久化背景色；文字色按背景亮度自动计算（深底浅字 / 浅底深字）。
const colorMenuOpen = ref(false)
function toggleColorMenu() {
  colorMenuOpen.value = !colorMenuOpen.value
}
function chooseColor(color: string) {
  closeMenus(true)
  emit('set-color', a.value.id, color)
}
function clearColor() {
  closeMenus(true)
  emit('set-color', a.value.id, '')
}
const cardStyle = computed(() => getCardVisualStyle(a.value.cardColor, a.value.status === 'stopped'))

</script>

<template>
  <article ref="cardElement" class="card card-motion" :class="['s-' + a.status]" :data-motion="a.status === 'running' && !a.restarting ? runningEffect : 'none'" :style="cardStyle" :aria-busy="a.restarting || a.status === 'starting' || undefined" @keydown="onEscape">
    <span v-if="(a.status === 'starting' || a.restarting) && startingEffect !== 'none'" class="startup-wake" aria-hidden="true"></span>
    <header class="head">
      <div class="name-row">
        <button class="ghost icon drag-handle" :title="tr('拖动排序或移到分组；Alt + 左右方向键调整顺序')" :aria-label="tr('拖动项目')" :disabled="moving" @pointerdown.stop.prevent="emit('drag-start', $event, a.id)" @keydown.alt.left.stop.prevent="emit('reorder-key', a.id, -1)" @keydown.alt.right.stop.prevent="emit('reorder-key', a.id, 1)">
          <UiIcon name="grip" :size="20" />
        </button>
        <h3 v-if="!editingName"><button ref="nameButton" type="button" class="name-trigger" :title="tr('单击修改名称')" @click="startRename">{{ a.name }}</button></h3>
        <input v-else ref="nameInput" v-model="nameDraft" class="name-edit" :aria-label="tr('项目名称')" @keydown.enter.prevent="commitRename(true)" @keydown.esc.stop.prevent="cancelRename" @blur="commitRename()" />
        <button v-if="cloudAlerts?.length" class="build-alert-badge" :class="{ failed: buildFailures.length }" :aria-label="tr('查看 {0} 的构建提醒（{1}）', [a.name, cloudAlerts.length])" :title="tr('点击查看失败版本和构建详情')" aria-haspopup="dialog" @click.stop="emit('cloud-details', a.id)"><UiIcon name="alert-circle" :size="13" /><span>{{ buildBadge }}</span><span v-if="cloudAlerts.length > 1" class="build-alert-count">{{ cloudAlerts.length }}</span></button>
      </div>
      <div class="identity-row">
        <span class="badge" :class="a.restarting ? 'starting' : a.status" role="status"><StartupIndicator v-if="a.status === 'starting' || a.restarting" :effect="startingEffect" /><span v-else class="dot"></span>{{ statusLabel }}</span>
        <span class="group-row">{{ groups.find(group => group.id === a.groupId)?.name || tr('未分组') }}</span>
      </div>
    </header>

    <div class="meta">
      <div v-if="sortedServices.length" class="services-heading"><span>{{ tr('服务与端口') }}</span><span>{{ sortedServices.length }}</span></div>
      <div v-if="sortedServices.length" class="services" :tabindex="sortedServices.length > 2 ? 0 : undefined" role="region" :aria-label="tr('服务与端口')">
        <div v-for="svc in sortedServices" :key="svc.id" class="svc-row">
          <div class="role-wrap" @focusout="onMenuFocusOut">
            <button class="role-btn" :disabled="svc.source === 'configured' || a.runtimeCheck?.state === 'running'" :class="{ locked: svc.roleSource === 'manual' }" :title="roleMeta(svc.role).label + (svc.roleSource === 'manual' ? tr('（已锁定）') : '') + tr(' — 点击切换')" :aria-label="roleMeta(svc.role).label + tr(' — 点击切换')" :aria-expanded="roleMenuOpen === svc.id" :aria-controls="`role-menu-${a.id}-${svc.id}`" @click.stop="toggleRoleMenu(svc.id, $event)">
              <UiIcon :name="roleMeta(svc.role).icon" :size="15" />
            </button>
            <div v-if="roleMenuOpen === svc.id" :id="`role-menu-${a.id}-${svc.id}`" class="role-menu" :style="roleMenuStyle" @click.stop>
              <button v-for="r in ROLE_OPTIONS" :key="r" :aria-pressed="(svc.role || 'unknown') === r" @click="pickRole(svc.id, r)">
                <UiIcon :name="ROLE_META[r].icon" :size="15" />
                <span>{{ ROLE_META[r].label }}</span>
                <UiIcon v-if="(svc.role || 'unknown') === r" name="check" :size="14" class="selected-role" />
              </button>
              <button class="reidentify" @click="reidentify(svc.id)"><UiIcon name="refresh" :size="14" />{{ tr('重新识别') }}</button>
            </div>
          </div>
          <span class="svc-dot" :class="svc.source === 'current' ? svc.health : 'inactive'" :title="serviceState(svc)" role="img" :aria-label="serviceState(svc)"></span>
          <a v-if="svc.url && svc.role !== 'database'" class="svc-url mono" :class="{ dim: svc.source !== 'current' }" :href="svc.url" :title="svc.url + ' · ' + serviceState(svc)" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
          <span v-else class="svc-url">{{ svc.role === 'database' ? tr('数据库') : tr('待发现服务地址') }}</span>
          <span v-if="svc.source !== 'current'" class="svc-source">{{ serviceState(svc) }}</span>
          <span class="svc-port mono">:{{ svc.port }}</span>
        </div>
      </div>
      <div v-if="a.lastUrl && !primaryURLInServices" class="meta-row url" :class="{ dim: !urlReachable }">
        <span class="k">{{ tr('访问地址') }}</span>
        <a class="v mono" :href="a.lastUrl" :title="a.lastUrl + (urlReachable ? '' : tr('（服务未运行）'))" @click.prevent="emit('open-url', a.id)">{{ a.lastUrl }}</a>
      </div>
      <div class="meta-row path" :title="a.entryScript">
        <span class="k">{{ tr('启动脚本') }}</span>
        <span class="v mono ellipsis">{{ a.entryScript }}</span>
      </div>
      <AppStartupNotice :app="a" :locked="runtimeLocked" :issue="startupIssue" :checking="checkingIssue" @log="emit('log', a.id)" />
    </div>

    <fieldset class="actions" :disabled="operationBusy">
      <div class="run-actions">
        <button v-if="canInspectConflict" :disabled="checkingIssue" aria-haspopup="dialog" @click="portDialog?.open()">{{ tr('处理端口占用') }}</button>
        <button v-else-if="runtimeLocked && (a.runtimeCheck?.state === 'running' || isActive)" aria-haspopup="dialog" @click="restartDialog?.open()"><UiIcon name="refresh" :size="14" />{{ tr('重启') }}</button>
        <template v-else-if="!runtimeLocked && a.restarting">
          <button class="stop-btn" disabled><UiIcon name="square" :size="14" />{{ tr('停止') }}</button>
          <button disabled><UiIcon name="refresh" :size="14" />{{ tr('重启中…') }}</button>
        </template>
        <template v-else-if="!runtimeLocked && isActive">
          <button class="stop-btn" :disabled="appsStore.operationBusy?.[a.id]" @click="emit('stop', a.id)"><UiIcon name="square" :size="14" />{{ tr('停止') }}</button>
          <button :disabled="appsStore.operationBusy?.[a.id]" @click="emit('restart', a.id)"><UiIcon name="refresh" :size="14" />{{ tr('重启') }}</button>
        </template>
        <button v-else-if="!runtimeLocked" class="primary" :disabled="checkingIssue || appsStore.operationBusy?.[a.id]" @click="emit('start', a.id)"><UiIcon :name="a.status === 'failed' ? 'refresh' : 'play'" :size="14" />{{ appsStore.operationBusy?.[a.id] ? tr('正在检查') : a.status === 'failed' ? tr('重新启动') : tr('启动') }}</button>
        <button class="ghost release-btn" :title="tr('Git 版本发布')" @click="emit('release', a.id)"><UiIcon name="upload" :size="14" />{{ tr('发布') }}</button>
      </div>
      <div class="utility-actions">
        <button class="ghost icon" :title="tr('查看日志')" :aria-label="tr('查看日志')" @click="emit('log', a.id)"><UiIcon name="log" /></button>
        <button class="ghost icon" :class="{ dim: a.lastUrl && !urlReachable }" :title="a.lastUrl ? (urlReachable ? tr('打开 URL') : tr('服务未运行，URL 可能无法访问')) : tr('暂无 URL')" :aria-label="tr('打开 URL')" :disabled="!a.lastUrl" @click="emit('open-url', a.id)"><UiIcon name="external-link" /></button>
        <button class="ghost icon" :title="tr('打开目录')" :aria-label="tr('打开目录')" @click="emit('open-dir', a.id)"><UiIcon name="folder" /></button>
        <details ref="manageDetails" class="manage" @toggle="onManageToggle" @focusout="onMenuFocusOut">
          <summary ref="manageSummary" :title="tr('更多操作')" :aria-label="tr('更多操作')" :aria-disabled="operationBusy || undefined" @click="operationBusy && $event.preventDefault()"><UiIcon name="more-vertical" :size="18" /></summary>
          <div class="manage-menu">
            <label class="menu-group" :for="`group-${a.id}`">
              <span>{{ tr('所在分组') }}</span>
              <select :id="`group-${a.id}`" class="group-select" :aria-label="tr('更改分组')" :value="a.groupId || ''" :disabled="moving" @change="chooseGroup">
                <option value="">{{ tr('未分组') }}</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </label>
            <details class="runtime-details">
              <summary><UiIcon name="server" :size="15" />{{ tr('运行详情') }}<UiIcon name="chevron-down" :size="13" /></summary>
              <dl><dt>{{ tr('状态') }}</dt><dd>{{ statusLabel }}</dd><dt>{{ tr('进程编号（PID）') }}</dt><dd class="mono">{{ a.pid || '—' }}</dd></dl>
              <div v-if="serviceDetails.history.length || serviceDetails.configured.length" class="service-details">
                <div v-if="serviceDetails.history.length" class="service-detail-group">
                  <span>{{ tr('历史端口（非本次运行）') }}</span>
                  <span class="mono">{{ serviceDetails.history.map(svc => `:${svc.port}`).join(' · ') }}</span>
                </div>
                <div v-if="serviceDetails.configured.length" class="service-detail-group">
                  <span>{{ tr('配置候选（未发现服务）') }}</span>
                  <span class="mono">{{ serviceDetails.configured.map(svc => `:${svc.port}`).join(' · ') }}</span>
                </div>
              </div>
            </details>
            <button @click="startRename"><UiIcon name="edit" :size="15" />{{ tr('改名') }}</button>
            <button :aria-expanded="colorMenuOpen" :aria-controls="`colors-${a.id}`" @click="toggleColorMenu"><UiIcon name="palette" :size="15" />{{ tr('卡片背景色') }}</button>
            <div v-if="colorMenuOpen" :id="`colors-${a.id}`" class="color-menu">
              <div class="palette">
                <button v-for="c in CARD_COLOR_PALETTE" :key="c" class="swatch" :class="{ active: normalizeHexColor(a.cardColor) === c }" :style="{ background: c }" :title="c" :aria-label="c" :aria-pressed="normalizeHexColor(a.cardColor) === c" @click="chooseColor(c)"></button>
              </div>
              <label class="custom-color">
                <input type="color" :value="normalizeHexColor(a.cardColor) || '#1e293b'" @input="chooseColor(($event.target as HTMLInputElement).value)" />
                <span>{{ tr('自定义') }}</span>
              </label>
              <button v-if="a.cardColor" class="clear-color" @click="clearColor">{{ tr('清除颜色') }}</button>
            </div>
            <button class="delete-action" @click="closeMenus(true); emit('delete', a.id)"><UiIcon name="trash" :size="15" />{{ tr('删除') }}</button>
          </div>
        </details>
      </div>
    </fieldset>
    <PortResolutionDialog ref="portDialog" :app-id="a.id" :app-name="a.name" @busy="resolvingPorts = $event" @start="emit('start', a.id)" />
    <RestartDialog ref="restartDialog" :app-id="a.id" :app-name="a.name" @busy="resolvingPorts = $event" @managed-restart="emit('restart', a.id)" />
  </article>
</template>

<style scoped>
.menu-group { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 8px; font-size: 12px; border-bottom: 1px solid var(--card-border, var(--border)); }
.menu-group .group-select:focus-visible { outline: 2px solid var(--card-fg, var(--accent)); outline-offset: 2px; }
.badge.checking, .badge.unknown { color: var(--card-status-amber, var(--amber)); }
.services-heading { display: flex; justify-content: space-between; color: var(--card-muted, var(--text-dim)); font-size: 11px; margin-bottom: 4px; }
.svc-source { margin-left: auto; white-space: nowrap; font-size: 10px; color: var(--card-muted, var(--text-faint)); }
.svc-dot.inactive { background: var(--card-muted, var(--text-faint)); }
.card {
  position: relative;
  background: var(--card-bg, var(--bg-elev));
  color: var(--card-fg, var(--text));
  border: 1px solid var(--card-border, var(--border));
  border-radius: var(--radius);
  padding: 16px 16px 10px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  transition: border-color 0.2s, background-color 0.3s;
}
.card:hover { border-color: var(--card-muted, var(--text-faint)); }
.card.s-stopped { background: var(--card-bg, color-mix(in srgb, var(--bg-elev) 62%, black)); }
.startup-wake { position: absolute; inset: 0; border-radius: inherit; pointer-events: none; background: linear-gradient(120deg, color-mix(in srgb, var(--card-glow, var(--accent)) 16%, transparent), transparent 70%); animation: startup-wake .65s ease-out both; }
@keyframes startup-wake { from { opacity: 1; } to { opacity: 0; } }
@media (prefers-reduced-motion: reduce) {
  .card { transition: none; }
  .startup-wake { display: none; animation: none; }
}
.card.s-degraded { --card-state-color: var(--card-status-amber, var(--amber)); }
.card.s-failed { --card-state-color: var(--card-status-red, var(--red)); }
.card:is(.s-degraded, .s-failed) {
  box-shadow: inset 0 3px 0 var(--card-state-color);
  border-color: color-mix(in srgb, var(--card-state-color) 45%, var(--card-bg, var(--bg-elev)));
}
.card:is(.s-degraded, .s-failed):hover { border-color: var(--card-state-color); }
.card :is(button, a, summary, select, input):focus-visible { outline: 2px solid var(--card-fg, var(--accent)); outline-offset: 3px; }
.head { display: flex; flex-direction: column; gap: 8px; }
.name-row { display: flex; align-items: flex-start; gap: 7px; min-width: 0; }
.name-row h3 { margin: 2px 0 0; font-size: 15px; line-height: 1.45; font-weight: 600; overflow-wrap: anywhere; color: var(--card-fg, var(--text)); }
.name-row h3, .name-row .name-edit { flex: 1; min-width: 0; }
.name-row .name-trigger { display: block; width: 100%; padding: 0; border: 0; border-radius: 3px; background: transparent; color: inherit; font: inherit; text-align: left; overflow-wrap: anywhere; }
.name-row .name-trigger:hover:not(:disabled) { background: transparent; text-decoration: none; }
.card .build-alert-badge { flex: 0 0 auto; margin-left: auto; display: inline-flex; align-items: center; gap: 5px; padding: 4px 7px; border-radius: 6px; border: 1px solid var(--card-status-amber, var(--amber)); background: var(--card-panel, var(--bg)); color: var(--card-status-amber, var(--amber)); font-size: 11px; line-height: 16px; white-space: nowrap; }
.card .build-alert-badge.failed { color: var(--card-status-red, var(--red)); border-color: var(--card-status-red, var(--red)); }
.card .build-alert-badge:hover { background: var(--card-panel, var(--bg-elev)); text-decoration: underline; }
.build-alert-count { font-weight: 700; }
.drag-handle { flex: 0 0 auto; cursor: grab; user-select: none; touch-action: none; color: var(--card-fg, var(--text)); }
/* Align the grip dots, rather than the SVG's internal padding, with the content edge. */
.card .drag-handle { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 4px; border: 1px solid transparent; border-radius: 6px; margin: -1px 0 0 -8px; }
.card .drag-handle:hover, .card .drag-handle:focus-visible { background: var(--card-panel, var(--bg)); border-color: transparent; outline: none; }
.drag-handle:active { cursor: grabbing; }
.name-edit { flex: 1; min-width: 0; font-size: 15px; font-weight: 600; padding: 3px 6px; color: var(--card-fg, var(--text)); background: var(--card-panel, var(--bg)); border-color: var(--card-border, var(--border)); border-radius: 4px; }
/* Inputs already have a border: use that edge for focus instead of a second ring. */
.card :is(.name-edit, .group-select):focus { border-color: var(--card-muted, var(--text-dim)); outline: none; box-shadow: none; }
.card :is(.name-edit, .group-select):focus-visible { outline: none; box-shadow: none; }
.identity-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.card .badge { padding: 0; background: transparent; border: 0; border-radius: 0; color: var(--card-muted, var(--text-dim)); font-size: 12px; white-space: nowrap; }
.card .badge.running { color: var(--card-running-text, var(--green)); }
.card .badge.starting, .card .badge.stopping, .card .badge.degraded { color: var(--card-status-amber, var(--amber)); }
.card .badge.failed { color: var(--card-status-red, var(--red)); }
.badge .dot { width: 6px; height: 6px; }
.card .badge:is(.starting, .stopping) .dot { flex: 0 0 auto; width: 10px; height: 10px; border: 1.5px solid currentColor; border-right-color: transparent; border-radius: 50%; background: transparent; animation: card-status-spin .9s linear infinite; }
@keyframes card-status-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) {
  .card .badge:is(.starting, .stopping) .dot { animation: none; }
}
.group-row { display: flex; align-items: center; gap: 6px; margin-left: auto; min-width: 0; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.group-select { max-width: 132px; min-width: 0; padding: 3px 4px; font-size: 11px; color: var(--card-muted, var(--text-dim)); border-color: transparent; border-radius: 4px; background: transparent; }
.group-select:hover { border-color: var(--card-border, var(--border)); }
.group-select option { color: var(--card-fg, var(--text)); background: var(--card-bg, var(--bg-elev)); }
.meta { display: flex; flex-direction: column; gap: 5px; font-size: 12px; }
.services { display: flex; flex-direction: column; gap: 5px; max-height: 61px; overflow: auto; padding: 7px 9px; margin-bottom: 4px; background: var(--card-panel, var(--bg)); border-radius: 5px; }
.services:focus-visible { outline: 2px solid var(--card-fg, var(--accent)); outline-offset: 2px; }
.services::-webkit-scrollbar { width: 6px; }
.services::-webkit-scrollbar-track { background: var(--card-panel, var(--bg)); border-radius: 5px; }
.services::-webkit-scrollbar-thumb { background: var(--card-muted, var(--text-dim)); border: 0; border-radius: 5px; min-height: 18px; }
.services::-webkit-scrollbar-thumb:hover { background-color: var(--card-fg, var(--text)); }
@supports not selector(::-webkit-scrollbar) {
  .services { scrollbar-color: var(--card-muted, var(--text-dim)) var(--card-panel, var(--bg)); }
}
.svc-row { display: flex; align-items: center; gap: 7px; flex-shrink: 0; font-size: 11px; min-width: 0; }
.svc-dot { width: 5px; height: 5px; border-radius: 50%; flex-shrink: 0; background: var(--card-muted, var(--text-faint)); }
.svc-dot.healthy { background: var(--card-status-green, var(--green)); }
.svc-dot.unhealthy { background: var(--card-status-red, var(--red)); }
.svc-dot.unknown { background: var(--card-status-amber, var(--amber)); }
.role-wrap { position: relative; display: inline-flex; flex-shrink: 0; }
.role-btn { display: flex; align-items: center; background: none; border: 1px solid transparent; border-radius: 4px; padding: 2px; color: var(--card-muted, var(--text-dim)); }
.role-btn.locked { border-color: var(--card-border, var(--border)); border-style: dashed; }
.role-menu, .manage-menu { position: absolute; z-index: 10; background: var(--bg-elev); color: var(--text); border: 1px solid var(--border); border-radius: 7px; box-shadow: var(--shadow); padding: 5px; display: flex; flex-direction: column; }
.role-menu { position: fixed; z-index: 30; min-width: 158px; max-width: calc(100vw - 16px); max-height: calc(100vh - 16px); overflow: auto; }
.role-menu button, .manage-menu > button { display: flex; align-items: center; gap: 9px; background: none; border: 0; text-align: left; padding: 8px; font-size: 12px; border-radius: 4px; color: var(--text-dim); }
.role-menu button:hover, .manage-menu > button:hover { background: var(--bg-elev-2); color: var(--text); }
.role-menu button:focus-visible, .manage-menu :is(button, input):focus-visible { outline-color: var(--accent); }
.selected-role { margin-left: auto; }
.role-menu .reidentify { border-top: 1px solid var(--border); border-radius: 0; margin-top: 4px; padding-top: 9px; }
.svc-port { color: var(--card-muted, var(--text-dim)); flex-shrink: 0; min-width: 43px; text-align: right; }
.svc-url { flex: 1; color: var(--card-link, #a9c3ef); cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; text-underline-offset: 3px; text-decoration: none; }
.svc-url:hover { text-decoration: underline; }
.svc-url.dim, .meta-row.url.dim .v { color: var(--card-muted, var(--text-dim)); }
button.dim { color: var(--card-muted, var(--text-dim)); }
.meta-row { display: flex; gap: 10px; align-items: baseline; min-width: 0; }
.meta-row .k { color: var(--card-muted, var(--text-dim)); width: 50px; flex-shrink: 0; font-size: 11px; }
.meta-row .v { color: var(--card-muted, var(--text-dim)); min-width: 0; word-break: break-all; font-size: 11px; line-height: 1.5; }
.meta-row.url .v { color: var(--card-link, #a9c3ef); cursor: pointer; text-decoration: none; }
.meta-row.url .v:hover { text-decoration: underline; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.actions { margin: auto 0 0; padding: 12px 0 0; min-width: 0; border: 0; border-top: 1px solid var(--card-border, var(--border)); display: flex; flex-direction: column; gap: 10px; }
.run-actions, .utility-actions { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.actions button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; flex-shrink: 0; }
.run-actions > button { min-height: 32px; padding: 6px 10px; font-size: 12px; }
.run-actions > button.primary { background: var(--card-action-bg, #2f68cb); border-color: var(--card-action-bg, #2f68cb); color: var(--card-action-fg, #fff); }
.run-actions > button.primary:hover:not(:disabled) { background: var(--card-action-hover, #3873d8); border-color: var(--card-action-hover, #3873d8); }
.run-actions > button:focus-visible { outline-color: var(--card-fg, var(--accent-hover)); }
.run-actions > button:not(.primary) { color: var(--card-fg, var(--text)); background: var(--card-panel, var(--bg-elev-2)); border-color: var(--card-border, var(--border)); }
.run-actions > button:not(.primary):hover:not(:disabled) { border-color: var(--card-muted, var(--accent)); }
.run-actions > button.stop-btn { border-color: var(--card-muted, var(--text-dim)); }
.run-actions > button.release-btn { margin-inline-start: auto; color: var(--card-muted, var(--text-dim)); background: transparent; border-color: transparent; }
.utility-actions { gap: 5px; }
.utility-actions > button { width: 30px; height: 28px; padding: 5px; color: var(--card-muted, var(--text-dim)); }
.card .role-btn:hover:not(:disabled),
.card .utility-actions > button:hover:not(:disabled) {
  background: var(--card-panel, var(--bg-elev-2));
  color: var(--card-fg, var(--text));
  border-color: var(--card-muted, var(--text-dim));
}
.manage { position: relative; margin-left: auto; }
.manage summary { display: flex; align-items: center; justify-content: flex-end; gap: 5px; padding: 6px 0 6px 6px; color: var(--card-muted, var(--text-dim)); font-size: 11px; cursor: pointer; list-style: none; border-radius: 3px; }
.manage > summary { width: 30px; height: 28px; justify-content: center; padding: 5px; border: 1px solid transparent; border-radius: 8px; }
.manage > summary:hover, .manage[open] > summary { background: var(--card-panel, var(--bg-elev-2)); border-color: var(--card-muted, var(--text-dim)); }
.manage > summary:focus-visible { outline-color: var(--card-fg, var(--accent-hover)); }
.manage summary::-webkit-details-marker { display: none; }
.manage summary[aria-disabled='true'] { opacity: .45; cursor: not-allowed; }
.manage[open] > summary { color: var(--card-fg, var(--text)); }
.manage-menu { right: 0; bottom: calc(100% + 6px); min-width: 210px; background: var(--card-bg, var(--bg-elev)); color: var(--card-fg, var(--text)); border-color: var(--card-border, var(--border)); }
.manage-menu > button, .runtime-details > summary { justify-content: flex-start; gap: 9px; text-align: left; color: var(--card-fg, var(--text)); }
.runtime-details > summary { padding: 8px; font-size: 12px; }
.manage-menu > button:hover, .runtime-details > summary:hover { background: var(--card-panel, var(--bg-elev-2)); color: var(--card-fg, var(--text)); }
.manage-menu :is(button, input, summary):focus-visible { outline-color: var(--card-fg, var(--accent)); }
.runtime-details > summary > :last-child { margin-left: auto; }
.runtime-details[open] > summary > :last-child { transform: rotate(180deg); }
.runtime-details dl { display: grid; grid-template-columns: 1fr auto; gap: 8px 12px; margin: 4px 8px 10px; font-size: 11px; }
.runtime-details dt { color: var(--card-muted, var(--text-dim)); }.runtime-details dd { margin: 0; color: var(--card-fg, var(--text)); }
.service-details { max-height: 140px; overflow-y: auto; margin: 8px; padding-top: 8px; border-top: 1px solid var(--card-border, var(--border)); }
.service-detail-group { display: grid; gap: 4px; font-size: 11px; color: var(--card-muted, var(--text-dim)); }
.service-detail-group + .service-detail-group { margin-top: 10px; }
.service-detail-group .mono { max-width: 210px; overflow-wrap: anywhere; color: var(--card-fg, var(--text)); }
.manage-menu > button.delete-action { color: #ff0000; background: transparent; border: 0; margin-top: 4px; padding: 8px; border-radius: 4px; }
.manage-menu > button.delete-action:hover { color: #ff0000; background: var(--card-panel, var(--bg-elev-2)); }
.color-menu { display: flex; flex-direction: column; gap: 9px; padding: 8px; }
.palette { display: grid; grid-template-columns: repeat(7, 1fr); gap: 5px; }
.actions .swatch { width: 21px; height: 21px; border-radius: 4px; border: 1px solid rgba(255,255,255,.25); cursor: pointer; padding: 0; }
.swatch.active { outline: 2px solid var(--accent); outline-offset: 2px; }
.custom-color { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--card-muted, var(--text-dim)); cursor: pointer; }
.custom-color input[type='color'] { width: 28px; height: 22px; padding: 0; border: 1px solid var(--border); border-radius: 4px; cursor: pointer; background: none; }
.color-menu .clear-color { justify-content: flex-start; text-align: left; background: none; border: 0; padding: 4px; font-size: 12px; color: var(--card-muted, var(--text-dim)); }
.color-menu .clear-color:hover { background: var(--card-panel, var(--bg-elev-2)); border-color: var(--card-border, var(--border)); }
@media (max-width: 420px) {
  .card { padding: 15px 14px 10px; }
  .group-select { max-width: 112px; }
}
</style>
