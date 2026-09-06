<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { AppView, Group, ServiceRole, StartupIssue } from '@/types'
import { api } from '@/api/http'
import { useAppsStore } from '@/stores/apps'
import UiIcon from '@/components/UiIcon.vue'
import { CARD_COLOR_PALETTE, getReadableTextColor, normalizeHexColor } from '@/utils/cardColors'

const props = defineProps<{ app: AppView; groups: Group[]; moving?: boolean }>()
const emit = defineEmits<{
  (e: 'start' | 'stop' | 'restart' | 'log' | 'open-dir' | 'release' | 'delete', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
  (e: 'set-color', id: string, color: string): void
  (e: 'drag-start', event: PointerEvent, id: string): void
  (e: 'move-group', id: string, groupId: string): void
}>()

const a = computed(() => props.app)
const startupIssue = ref<StartupIssue | null>(null)
const checkingIssue = ref(false)
const recovering = ref(false)
const recoveryError = ref('')
const issueDetails = ref<HTMLDetailsElement | null>(null)
watch(recoveryError, async (error) => {
  if (error) { await nextTick(); if (issueDetails.value) issueDetails.value.open = true }
})
const showStartupIssue = computed(() => a.value.status === 'failed' || recovering.value)
// Empty conflicts only confirm a free port after a successful ownership check.
const portsReleased = computed(() => startupIssue.value?.code === 'port_in_use'
  && startupIssue.value.canRecover && startupIssue.value.conflicts.length === 0)
const issueTitle = computed(() => {
  if (recovering.value) return tr('正在重新启动…')
  if (checkingIssue.value) return tr('正在检查失败原因…')
  if (portsReleased.value) return tr('可以重新启动')
  if (startupIssue.value?.conflicts.length) return tr('端口 {0} 被占用', [[...new Set(startupIssue.value.conflicts.map(conflict => conflict.port))].join('、')])
  return tr('启动失败')
})
const issueDescription = computed(() => {
  if (recovering.value) return checkingIssue.value ? tr('正在检查占用进程…') : ''
  if (checkingIssue.value) return ''
  if (portsReleased.value) return tr('端口 {0} 已释放。', [startupIssue.value!.ports.join('、')])
  if (startupIssue.value?.reason) return tr(startupIssue.value.reason)
  if (startupIssue.value?.canRecover && startupIssue.value.conflicts.length) return tr('将关闭本项目占用进程，再自动启动')
  return tr('打开日志查看失败原因。')
})
let issueRequest = 0
async function checkStartupIssue() {
  const request = ++issueRequest
  if (a.value.status !== 'failed') { startupIssue.value = null; checkingIssue.value = false; recoveryError.value = ''; return }
  checkingIssue.value = true
  try {
    const result = await api.startupIssue(a.value.id)
    if (request === issueRequest) startupIssue.value = result
  } catch {
    if (request === issueRequest) startupIssue.value = null
  } finally { if (request === issueRequest) checkingIssue.value = false }
}
watch(() => [a.value.id, a.value.status, a.value.runId], checkStartupIssue, { immediate: true })
async function recoverPorts() {
  if (recovering.value) return
  recovering.value = true
  recoveryError.value = ''
  try {
    // Refresh the process identity; never submit stale PID information from a card.
    await checkStartupIssue()
    const issue = startupIssue.value
    if (!issue?.canRecover) throw new Error(issue?.reason || tr('无法安全释放端口，请查看日志'))
    await api.recoverPorts(a.value.id, issue.fingerprint)
    await useAppsStore().load()
  } catch (error: any) {
    recoveryError.value = error?.message || String(error)
    await checkStartupIssue()
  } finally { recovering.value = false }
}

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
  if (restoreFocus) { await nextTick(); manageSummary.value?.focus() }
}
function cancelRename() {
  nameDraft.value = a.value.name
  editingName.value = false
  manageSummary.value?.focus()
}

const isActive = computed(
  () => ['starting', 'running', 'degraded', 'stopping'].includes(a.value.status)
)
// URL 仅在服务运行时才可达；停止/失败时置灰，避免点了浏览器显示无法访问造成"链接坏了"的误会。
const urlReachable = computed(() => isActive.value)

const statusLabel = computed(() => {
  if (a.value.restarting) return tr("重启中")
  const m: Record<string, string> = {
    starting: tr("启动中"),
    running: tr("运行中"),
    degraded: tr("降级"),
    stopping: tr("停止中"),
    stopped: tr("已停止"),
    failed: tr("失败"),
  }
  return m[a.value.status] || a.value.status
})

// 服务健康状态文本
function healthText(h: string): string {
  const m: Record<string, string> = {
    healthy: tr("健康"),
    unhealthy: tr("不健康"),
    unknown: tr("检测中"),
  }
  return m[h] || h
}

// 展示顺序：前端 > 后端 > 数据库 > 未识别，同角色按端口
const ROLE_ORDER: Record<ServiceRole, number> = {
  frontend: 0,
  backend: 1,
  database: 2,
  unknown: 3,
}
const sortedServices = computed(() => {
  const list = [...(a.value.services || [])]
  return list.sort((x, y) => {
    const rx = ROLE_ORDER[(x.role as ServiceRole) || 'unknown'] ?? 9
    const ry = ROLE_ORDER[(y.role as ServiceRole) || 'unknown'] ?? 9
    if (rx !== ry) return rx - ry
    return (x.port || 0) - (y.port || 0)
  })
})

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
const cardStyle = computed(() => {
  const bg = normalizeHexColor(a.value.cardColor)
  if (!bg) return {}
  const luminance = (color: string) => {
    const channels = [1, 3, 5].map((start) => parseInt(color.slice(start, start + 2), 16) / 255)
      .map((value) => value <= 0.04045 ? value / 12.92 : Math.pow((value + 0.055) / 1.055, 2.4))
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
  }
  const bgLuminance = luminance(bg)
  const contrast = (color: string) => {
    const value = luminance(color)
    return (Math.max(value, bgLuminance) + 0.05) / (Math.min(value, bgLuminance) + 0.05)
  }
  // The legacy brightness helper can choose white on medium gray; retain the
  // saved background while guaranteeing readable text for custom colors.
  let fg = getReadableTextColor(bg)
  if (contrast(fg) < 4.5) fg = contrast('#111827') >= contrast('#f8fafc') ? '#111827' : '#f8fafc'
  if (contrast(fg) < 4.5) fg = bgLuminance > 0.179 ? '#000000' : '#ffffff'
  const readable = (color: string) => contrast(color) >= 4.5 ? color : fg
  const darkText = luminance(fg) < 0.1
  return {
    '--card-bg': bg,
    '--card-fg': fg,
    '--card-muted': readable(darkText ? '#475569' : '#cbd5e1'),
    '--card-panel': darkText ? 'rgba(255, 255, 255, 0.12)' : 'rgba(0, 0, 0, 0.08)',
    '--card-border': darkText ? 'rgba(17, 24, 39, 0.18)' : 'rgba(255, 255, 255, 0.16)',
    '--card-link': fg,
    '--card-status-green': readable(darkText ? '#166534' : '#a7f3d0'),
    '--card-status-amber': readable(darkText ? '#854d0e' : '#fde68a'),
    '--card-status-red': readable(darkText ? '#991b1b' : '#fecaca'),
  } as Record<string, string>
})
</script>

<template>
  <article ref="cardElement" class="card" :class="['s-' + a.status]" :style="cardStyle" :aria-busy="a.restarting || undefined" @keydown="onEscape">
    <header class="head">
      <div class="name-row">
        <button class="ghost icon drag-handle" :title="tr('拖动可排序，或移到侧栏分组')" :aria-label="tr('拖动项目')" :disabled="moving" @pointerdown.stop.prevent="emit('drag-start', $event, a.id)">
          <UiIcon name="grip" :size="17" />
        </button>
        <h3 v-if="!editingName" :title="a.name" @dblclick="startRename">{{ a.name }}</h3>
        <input v-else ref="nameInput" v-model="nameDraft" class="name-edit" :aria-label="tr('项目名称')" @keydown.enter.prevent="commitRename(true)" @keydown.esc.stop.prevent="cancelRename" @blur="commitRename()" />
      </div>
      <div class="identity-row">
        <span class="badge" :class="a.restarting ? 'starting' : a.status"><span class="dot"></span>{{ statusLabel }}</span>
        <div class="group-row">
          <label :for="`group-${a.id}`">{{ tr('分组') }}</label>
          <select :id="`group-${a.id}`" class="group-select" :value="a.groupId || ''" :disabled="moving" @change="chooseGroup">
            <option value="">{{ tr('未分组') }}</option>
            <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
          </select>
        </div>
      </div>
    </header>

    <div class="meta">
      <div v-if="a.services && a.services.length" class="services" :tabindex="a.services.length > 2 ? 0 : undefined" :role="a.services.length > 2 ? 'region' : undefined" :aria-label="tr('项目端口')">
        <div v-for="svc in sortedServices" :key="svc.id" class="svc-row">
          <div class="role-wrap" @focusout="onMenuFocusOut">
            <button class="role-btn" :class="{ locked: svc.roleSource === 'manual' }" :title="roleMeta(svc.role).label + (svc.roleSource === 'manual' ? tr('（已锁定）') : '') + tr(' — 点击切换')" :aria-label="roleMeta(svc.role).label + tr(' — 点击切换')" :aria-expanded="roleMenuOpen === svc.id" :aria-controls="`role-menu-${a.id}-${svc.id}`" @click.stop="toggleRoleMenu(svc.id, $event)">
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
          <span class="svc-dot" :class="svc.health" :title="healthText(svc.health)" role="img" :aria-label="healthText(svc.health)"></span>
          <span class="svc-port mono">:{{ svc.port }}</span>
          <a class="svc-url mono" :class="{ dim: !urlReachable }" :href="svc.url" :title="svc.url + (urlReachable ? '' : tr('（服务未运行）'))" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
        </div>
      </div>
      <div v-else-if="a.lastUrl" class="meta-row url" :class="{ dim: !urlReachable }">
        <span class="k">URL</span>
        <a class="v mono" :href="a.lastUrl" :title="a.lastUrl + (urlReachable ? '' : tr('（服务未运行）'))" @click.prevent="emit('open-url', a.id)">{{ a.lastUrl }}</a>
      </div>
      <div class="meta-row path" :title="a.entryScript">
        <span class="k">{{ tr('入口') }}</span>
        <span class="v mono ellipsis">{{ a.entryScript }}</span>
      </div>
      <div class="meta-row pid-row">
        <span class="k">PID</span>
        <span class="v mono">{{ a.pid || '—' }}</span>
      </div>
    <details v-if="showStartupIssue" ref="issueDetails" class="startup-error" :class="{ resolved: portsReleased && !recoveryError, pending: checkingIssue || recovering }" :aria-busy="checkingIssue || recovering">
      <summary>
        <UiIcon class="issue-icon" :name="checkingIssue || recovering ? 'refresh' : portsReleased && !recoveryError ? 'check-circle' : 'alert-circle'" :size="16" />
        <strong role="status" aria-live="polite">{{ issueTitle }}</strong>
        <UiIcon class="issue-chevron" name="chevron-down" :size="13" />
      </summary>
      <div class="issue-content">
        <p v-if="issueDescription">{{ issueDescription }}</p>
        <p v-if="recoveryError" class="recovery-error" role="alert">{{ recoveryError }}</p>
        <details v-if="startupIssue?.conflicts.length" class="conflict-details">
          <summary>{{ tr('占用详情') }}<UiIcon name="chevron-down" :size="12" /></summary>
          <div v-for="c in startupIssue.conflicts" :key="`${c.port}-${c.pid}`" class="mono">:{{ c.port }} · {{ c.name }} · PID {{ c.pid }}</div>
        </details>
        <div class="issue-links">
          <button v-if="startupIssue?.code === 'port_in_use' && !startupIssue.canRecover" class="ghost" :disabled="recovering || checkingIssue" @click="checkStartupIssue">{{ tr('重新检查') }}</button>
          <button class="ghost" :disabled="recovering" @click="emit('log', a.id)">{{ tr('查看日志') }}<UiIcon name="arrow-right" :size="12" /></button>
        </div>
      </div>
    </details>
    </div>

    <fieldset class="actions" :disabled="recovering">
      <div class="run-actions">
        <button v-if="recovering || (a.status === 'failed' && startupIssue?.canRecover)" class="primary" :disabled="recovering || checkingIssue" @click="recoverPorts"><UiIcon name="refresh" :size="14" />{{ recovering ? tr('处理中…') : startupIssue?.conflicts.length ? tr('释放端口并重试') : tr('重新启动') }}</button>
        <template v-else-if="a.restarting">
          <button class="stop-btn" disabled><UiIcon name="square" :size="14" />{{ tr('停止') }}</button>
          <button disabled><UiIcon name="refresh" :size="14" />{{ tr('重启中…') }}</button>
        </template>
        <template v-else-if="isActive">
          <button class="stop-btn" @click="emit('stop', a.id)"><UiIcon name="square" :size="14" />{{ tr('停止') }}</button>
          <button @click="emit('restart', a.id)"><UiIcon name="refresh" :size="14" />{{ tr('重启') }}</button>
        </template>
        <button v-else class="primary" :disabled="checkingIssue" @click="emit('start', a.id)"><UiIcon :name="a.status === 'failed' ? 'refresh' : 'play'" :size="14" />{{ a.status === 'failed' ? tr('重新启动') : tr('启动') }}</button>
        <button class="ghost release-btn" :title="tr('Git 版本发布')" @click="emit('release', a.id)"><UiIcon name="upload" :size="14" />{{ tr('发布') }}</button>
      </div>
      <div class="utility-actions">
        <button v-if="!showStartupIssue" class="ghost icon" :title="tr('查看日志')" :aria-label="tr('查看日志')" @click="emit('log', a.id)"><UiIcon name="log" /></button>
        <button class="ghost icon" :class="{ dim: a.lastUrl && !urlReachable }" :title="a.lastUrl ? (urlReachable ? tr('打开 URL') : tr('服务未运行，URL 可能无法访问')) : tr('暂无 URL')" :aria-label="tr('打开 URL')" :disabled="!a.lastUrl" @click="emit('open-url', a.id)"><UiIcon name="external-link" /></button>
        <button class="ghost icon" :title="tr('打开目录')" :aria-label="tr('打开目录')" @click="emit('open-dir', a.id)"><UiIcon name="folder" /></button>
        <details ref="manageDetails" class="manage" @toggle="onManageToggle" @focusout="onMenuFocusOut">
          <summary ref="manageSummary" :aria-disabled="recovering || undefined" @click="recovering && $event.preventDefault()">{{ tr('更多操作') }}<UiIcon name="chevron-down" :size="13" /></summary>
          <div class="manage-menu">
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
  </article>
</template>

<style scoped>
.card {
  background: var(--card-bg, var(--bg-elev));
  color: var(--card-fg, var(--text));
  border: 1px solid var(--card-border, var(--border));
  border-radius: var(--radius);
  padding: 16px 16px 10px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  transition: border-color 0.15s;
}
.card:hover { border-color: var(--card-muted, var(--text-faint)); }
.card :is(button, a, summary, select, input):focus-visible { outline: 2px solid var(--card-fg, var(--accent)); outline-offset: 3px; }
.head { display: flex; flex-direction: column; gap: 8px; }
.name-row { display: flex; align-items: flex-start; gap: 7px; min-width: 0; }
.name-row h3 { margin: 2px 0 0; font-size: 15px; line-height: 1.45; font-weight: 600; overflow-wrap: anywhere; color: var(--card-fg, var(--text)); }
.drag-handle { flex: 0 0 auto; cursor: grab; user-select: none; touch-action: none; color: var(--card-muted, var(--text-faint)); }
.card .drag-handle { padding: 3px 0; border: 0; }
.drag-handle:active { cursor: grabbing; }
.name-edit { flex: 1; min-width: 0; font-size: 15px; font-weight: 600; padding: 3px 6px; }
.identity-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.card .badge { padding: 0; background: transparent; border: 0; border-radius: 0; color: var(--card-muted, var(--text-dim)); font-size: 12px; white-space: nowrap; }
.card .badge.running { color: var(--card-status-green, var(--green)); }
.card .badge.starting, .card .badge.degraded { color: var(--card-status-amber, var(--amber)); }
.card .badge.failed { color: var(--card-status-red, var(--red)); }
.badge .dot { width: 6px; height: 6px; }
.group-row { display: flex; align-items: center; gap: 6px; margin-left: auto; min-width: 0; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.group-row label { flex-shrink: 0; }
.group-select { max-width: 132px; min-width: 0; padding: 3px 4px; font-size: 11px; color: var(--card-muted, var(--text-dim)); border-color: transparent; background: transparent; }
.group-select:hover { border-color: var(--card-border, var(--border)); }
.group-select option { color: var(--text); background: var(--bg-elev); }
.meta { display: flex; flex-direction: column; gap: 5px; font-size: 12px; }
.services { display: flex; flex-direction: column; gap: 5px; max-height: 61px; overflow: auto; padding: 7px 9px; margin-bottom: 4px; background: var(--card-panel, var(--bg)); border-radius: 5px; }
.services:focus-visible { outline: 2px solid var(--card-fg, var(--accent)); outline-offset: 2px; }
.services::-webkit-scrollbar { width: 6px; }
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
.svc-port { color: var(--card-muted, var(--text-dim)); flex-shrink: 0; min-width: 43px; }
.svc-url { color: var(--card-link, #a9c3ef); cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; text-underline-offset: 3px; text-decoration: none; }
.svc-url:hover { text-decoration: underline; }
.svc-url.dim, .meta-row.url.dim .v { color: var(--card-muted, var(--text-dim)); }
button.dim { color: var(--card-muted, var(--text-dim)); }
.meta-row { display: flex; gap: 10px; align-items: baseline; min-width: 0; }
.meta-row .k { color: var(--card-muted, var(--text-dim)); width: 28px; flex-shrink: 0; font-size: 11px; }
.meta-row .v { color: var(--card-muted, var(--text-dim)); min-width: 0; word-break: break-all; font-size: 11px; line-height: 1.5; }
.meta-row.url .v { color: var(--card-link, #a9c3ef); cursor: pointer; text-decoration: none; }
.meta-row.url .v:hover { text-decoration: underline; }
.ellipsis { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.actions { margin: auto 0 0; padding: 12px 0 0; min-width: 0; border: 0; border-top: 1px solid var(--card-border, var(--border)); display: flex; flex-direction: column; gap: 10px; }
.run-actions, .utility-actions { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.actions button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; flex-shrink: 0; }
.run-actions > button { min-height: 32px; padding: 6px 10px; font-size: 12px; }
.run-actions > button:not(.primary) { color: var(--card-fg, var(--text)); background: var(--card-panel, var(--bg-elev-2)); border-color: var(--card-border, var(--border)); }
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
.manage summary::-webkit-details-marker { display: none; }
.manage summary[aria-disabled='true'] { opacity: .45; cursor: not-allowed; }
.manage[open] summary { color: var(--card-fg, var(--text)); }
.manage-menu { right: 0; bottom: calc(100% + 6px); min-width: 210px; }
.manage-menu > button.delete-action { color: var(--red); border-top: 1px solid var(--border); margin-top: 4px; padding-top: 10px; border-radius: 0; }
.color-menu { display: flex; flex-direction: column; gap: 9px; padding: 8px; }
.palette { display: grid; grid-template-columns: repeat(7, 1fr); gap: 5px; }
.actions .swatch { width: 21px; height: 21px; border-radius: 4px; border: 1px solid rgba(255,255,255,.25); cursor: pointer; padding: 0; }
.swatch.active { outline: 2px solid var(--accent); outline-offset: 2px; }
.custom-color { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--text-dim); cursor: pointer; }
.custom-color input[type='color'] { width: 28px; height: 22px; padding: 0; border: 1px solid var(--border); border-radius: 4px; cursor: pointer; background: none; }
.clear-color { background: none; border: 0; padding: 4px; font-size: 12px; color: var(--text-dim); }
.startup-error { font-size: 12px; line-height: 1.55; overflow-wrap: anywhere; }
.startup-error > summary { display: flex; align-items: center; gap: 7px; padding: 2px 0; border-radius: 3px; cursor: pointer; list-style: none; }
.startup-error > summary::-webkit-details-marker { display: none; }
.startup-error > summary strong { color: var(--card-fg, var(--text)); font-size: 12px; font-weight: 500; }
.issue-chevron { margin-left: auto; color: var(--card-muted, var(--text-dim)); }
.startup-error[open] > summary .issue-chevron { transform: rotate(180deg); }
.issue-icon { color: var(--card-status-red, var(--red)); }
.resolved .issue-icon { color: var(--card-status-green, var(--green)); }
.pending .issue-icon { color: var(--card-muted, var(--text-dim)); }
.issue-content { min-width: 0; padding: 4px 0 2px 23px; }
.issue-content p { margin: 4px 0 0; color: var(--card-muted, var(--text-dim)); }
.issue-content .recovery-error { color: var(--card-status-red, var(--red)); }
.issue-links { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 7px; }
.issue-links > button { display: inline-flex; align-items: center; gap: 5px; padding: 2px 0; border: 0; border-radius: 2px; background: transparent; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.issue-links > button:hover:not(:disabled) { color: var(--card-fg, var(--text)); background: transparent; text-decoration: underline; text-underline-offset: 3px; }
.conflict-details { margin-top: 6px; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.conflict-details summary { display: inline-flex; align-items: center; gap: 4px; border-radius: 2px; cursor: pointer; list-style: none; }
.conflict-details summary::-webkit-details-marker { display: none; }
.conflict-details[open] summary .ui-icon { transform: rotate(180deg); }
.conflict-details .mono { margin-top: 5px; }
@media (max-width: 420px) {
  .card { padding: 15px 14px 10px; }
  .group-select { max-width: 112px; }
}
</style>
