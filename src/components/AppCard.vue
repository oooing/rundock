<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { AppView, ServiceRole } from '@/types'
import backendIcon from '@/assets/backend-server.svg'
import { CARD_COLOR_PALETTE, getReadableTextColor, normalizeHexColor } from '@/utils/cardColors'

const props = defineProps<{ app: AppView }>()
const emit = defineEmits<{
  (e: 'start' | 'stop' | 'restart' | 'log' | 'open-dir' | 'delete', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
  (e: 'set-color', id: string, color: string): void
  (e: 'drag-start', event: PointerEvent, id: string): void
}>()

const a = computed(() => props.app)

// 服务角色 → 图标/颜色/标签
const ROLE_META: Record<ServiceRole, { icon: string; iconSrc?: string; color: string; label: string }> = {
  frontend: { icon: '🌐', color: '#3b82f6', label: '前端' },
  backend: { icon: '', iconSrc: backendIcon, color: '#8b5cf6', label: '后端' },
  database: { icon: '🗄️', color: '#f59e0b', label: '数据库' },
  unknown: { icon: '❓', color: '#9ca3af', label: '未识别' },
}
const ROLE_OPTIONS: ServiceRole[] = ['frontend', 'backend', 'database', 'unknown']

// 当前展开切换菜单的 service id（null = 无）
const roleMenuOpen = ref<string | null>(null)
// role 可能为 undefined（老数据/未填充），默认回退到 unknown，避免 ROLE_META[undefined] 崩溃。
function roleMeta(role?: ServiceRole) {
  return ROLE_META[role ?? 'unknown']
}
function toggleRoleMenu(svcId: string) {
  roleMenuOpen.value = roleMenuOpen.value === svcId ? null : svcId
}
function pickRole(svcId: string, role: ServiceRole) {
  roleMenuOpen.value = null
  emit('set-role', a.value.id, svcId, role)
}
function reidentify(svcId: string) {
  roleMenuOpen.value = null
  emit('reidentify', a.value.id, svcId)
}

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
function startRename() {
  nameDraft.value = a.value.name
  editingName.value = true
}
function commitRename() {
  const n = nameDraft.value.trim()
  if (n && n !== a.value.name) {
    emit('rename', a.value.id, n)
  }
  editingName.value = false
}

const isActive = computed(
  () => ['starting', 'running', 'degraded', 'stopping'].includes(a.value.status)
)
// URL 仅在服务运行时才可达；停止/失败时置灰，避免点了浏览器显示无法访问造成"链接坏了"的误会。
const urlReachable = computed(() => isActive.value)

const statusLabel = computed(() => {
  if (a.value.restarting) return '重启中'
  const m: Record<string, string> = {
    starting: '启动中',
    running: '运行中',
    degraded: '降级',
    stopping: '停止中',
    stopped: '已停止',
    failed: '失败',
  }
  return m[a.value.status] || a.value.status
})

// 服务健康状态文本
function healthText(h: string): string {
  const m: Record<string, string> = {
    healthy: '健康',
    unhealthy: '不健康',
    unknown: '检测中',
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
  colorMenuOpen.value = false
  emit('set-color', a.value.id, color)
}
function clearColor() {
  colorMenuOpen.value = false
  emit('set-color', a.value.id, '')
}
const cardStyle = computed(() => {
  const bg = normalizeHexColor(a.value.cardColor)
  if (!bg) return {}
  const fg = getReadableTextColor(bg)
  const darkText = fg === '#111827'
  return {
    '--card-bg': bg,
    '--card-fg': fg,
    '--card-muted': darkText ? 'rgba(17, 24, 39, 0.70)' : 'rgba(248, 250, 252, 0.72)',
    '--card-panel': darkText ? 'rgba(17, 24, 39, 0.07)' : 'rgba(255, 255, 255, 0.08)',
    '--card-border': darkText ? 'rgba(17, 24, 39, 0.18)' : 'rgba(255, 255, 255, 0.16)',
  } as Record<string, string>
})
</script>

<template>
  <article class="card" :class="['s-' + a.status]" :style="cardStyle" :aria-busy="a.restarting || undefined">
    <header class="head">
      <div class="name-row">
        <button
          class="ghost icon drag-handle"
          title="按住拖拽调整卡片顺序"
          aria-label="拖拽调整卡片顺序"
          @pointerdown.stop.prevent="emit('drag-start', $event, a.id)"
        >⠿</button>
        <h3 v-if="!editingName" :title="a.name + '（点✎改名）'" @dblclick="startRename">{{ a.name }}</h3>
        <input
          v-else
          v-model="nameDraft"
          class="name-edit"
          @keyup.enter="commitRename"
          @blur="commitRename"
          @vue:mounted="($event as any).el?.focus?.()"
          autofocus
        />
      </div>
      <div class="head-right">
        <button v-if="!editingName" class="ghost icon rename-btn" title="改名" @click="startRename">✎</button>
        <div class="color-wrap">
          <button
            class="ghost icon color-btn"
            title="卡片背景色"
            @click.stop="toggleColorMenu"
          >🎨</button>
          <div v-if="colorMenuOpen" class="color-backdrop" @click="colorMenuOpen = false"></div>
          <div v-if="colorMenuOpen" class="color-menu" @click.stop>
            <div class="palette">
              <button
                v-for="c in CARD_COLOR_PALETTE"
                :key="c"
                class="swatch"
                :class="{ active: normalizeHexColor(a.cardColor) === c }"
                :style="{ background: c }"
                :title="c"
                @click="chooseColor(c)"
              ></button>
            </div>
            <label class="custom-color">
              <input type="color" :value="normalizeHexColor(a.cardColor) || '#1e293b'" @input="chooseColor(($event.target as HTMLInputElement).value)" />
              <span>自定义</span>
            </label>
            <button v-if="a.cardColor" class="clear-color" @click="clearColor">清除颜色</button>
          </div>
        </div>
        <span class="badge" :class="a.restarting ? 'starting' : a.status">
          <span class="dot"></span>{{ statusLabel }}
        </span>
      </div>
    </header>

    <div class="meta">
      <!-- 多服务列表：每个服务一行（角色图标 + 健康点 + 端口 + URL） -->
      <div v-if="a.services && a.services.length" class="services">
        <!-- 切换菜单遮罩：点外部关闭 -->
        <div v-if="roleMenuOpen" class="role-backdrop" @click="roleMenuOpen = null"></div>
        <div v-for="svc in sortedServices" :key="svc.id" class="svc-row">
          <div class="role-wrap">
            <button
              class="role-btn"
              :class="{ locked: svc.roleSource === 'manual' }"
              :style="{ color: roleMeta(svc.role).color }"
              :title="roleMeta(svc.role).label + (svc.roleSource === 'manual' ? '（已锁定）' : '') + ' — 点击切换'"
              @click.stop="toggleRoleMenu(svc.id)"
            >
              <img v-if="roleMeta(svc.role).iconSrc" class="role-icon" :src="roleMeta(svc.role).iconSrc" alt="" />
              <template v-else>{{ roleMeta(svc.role).icon }}</template>
            </button>
            <div v-if="roleMenuOpen === svc.id" class="role-menu" @click.stop>
              <button
                v-for="r in ROLE_OPTIONS"
                :key="r"
                :style="{ color: ROLE_META[r].color }"
                @click="pickRole(svc.id, r)"
              >
                <img v-if="ROLE_META[r].iconSrc" class="role-icon" :src="ROLE_META[r].iconSrc" alt="" />
                <span v-else>{{ ROLE_META[r].icon }}</span>
                {{ ROLE_META[r].label }}
              </button>
              <button class="reidentify" @click="reidentify(svc.id)">🔄 重新识别</button>
            </div>
          </div>
          <span class="svc-dot" :class="svc.health" :title="healthText(svc.health)"></span>
          <span class="svc-port mono">:{{ svc.port }}</span>
          <a class="svc-url mono" :class="{ dim: !urlReachable }" :title="svc.url + (urlReachable ? '' : '（服务未运行）')" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
        </div>
      </div>
      <!-- 无服务时回退到旧的 lastUrl -->
      <div class="meta-row url" :class="{ dim: !urlReachable }" v-else-if="a.lastUrl">
        <span class="k">URL</span>
        <a class="v mono" :title="a.lastUrl + (urlReachable ? '' : '（服务未运行）')" @click.prevent="emit('open-url', a.id)">{{ a.lastUrl }}</a>
      </div>
      <div class="meta-row">
        <span class="k">PID</span>
        <span class="v mono">{{ a.pid || '—' }}</span>
      </div>
      <div class="meta-row path" :title="a.entryScript">
        <span class="k">入口</span>
        <span class="v mono ellipsis">{{ a.entryScript }}</span>
      </div>
    </div>

    <footer class="actions">
      <template v-if="a.restarting">
        <button class="danger" disabled>停止</button>
        <button disabled>重启中…</button>
      </template>
      <template v-else-if="isActive">
        <button @click="emit('stop', a.id)" class="danger">停止</button>
        <button @click="emit('restart', a.id)">重启</button>
      </template>
      <template v-else>
        <button class="primary" @click="emit('start', a.id)">启动</button>
      </template>
      <button class="ghost icon" title="查看日志" @click="emit('log', a.id)">📜</button>
      <button
        class="ghost icon"
        :class="{ dim: a.lastUrl && !urlReachable }"
        :title="a.lastUrl ? (urlReachable ? '打开 URL' : '服务未运行，URL 可能无法访问') : '暂无 URL'"
        :disabled="!a.lastUrl"
        @click="emit('open-url', a.id)"
      >🌐</button>
      <button class="ghost icon" title="打开目录" @click="emit('open-dir', a.id)">📁</button>
      <button class="ghost icon danger-ico" title="删除" @click="emit('delete', a.id)">🗑</button>
    </footer>
  </article>
</template>

<style scoped>
.card {
  background: var(--card-bg, var(--bg-elev));
  color: var(--card-fg, var(--text));
  border: 1px solid var(--card-border, var(--border));
  border-radius: var(--radius);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.card:hover {
  border-color: var(--accent);
  box-shadow: var(--shadow);
}
.card.s-failed {
  border-color: rgba(248, 113, 113, 0.4);
}
.card.s-running {
  border-color: rgba(52, 211, 153, 0.3);
}
.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}
.head-right {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.drag-handle {
  cursor: grab;
  user-select: none;
  touch-action: none;
  font-size: 17px;
  line-height: 1;
}
.drag-handle:active {
  cursor: grabbing;
}
.name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}
.name-row h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: pointer;
  color: var(--card-fg, var(--text));
}
.name-edit {
  flex: 1;
  min-width: 0;
  font-size: 15px;
  font-weight: 600;
  padding: 2px 6px;
}
.rename-btn {
  opacity: 0;
  transition: opacity 0.15s;
}
.card:hover .rename-btn {
  opacity: 0.7;
}
.rename-btn:hover {
  opacity: 1 !important;
}
.meta {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}
/* 多服务列表 */
.services {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 8px 10px;
  background: var(--card-panel, var(--bg));
  border-radius: 6px;
  border: 1px solid var(--card-border, var(--border));
}
.svc-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  min-width: 0;
}
.svc-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--text-faint);
}
.svc-dot.healthy {
  background: var(--green);
}
.svc-dot.unhealthy {
  background: var(--red);
}
.svc-dot.unknown {
  background: var(--amber);
}
/* 角色图标 + 切换菜单 */
.role-wrap {
  position: relative;
  display: inline-flex;
}
.role-btn {
  background: none;
  border: 1px solid transparent;
  border-radius: 4px;
  padding: 0 2px;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
}
.role-icon {
  display: block;
  width: 16px;
  height: 16px;
}
.role-btn.locked {
  border-color: currentColor;
  border-style: dashed;
}
.role-backdrop {
  position: fixed;
  inset: 0;
  z-index: 5;
}
.role-menu {
  position: absolute;
  left: 0;
  top: 100%;
  z-index: 10;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: var(--shadow);
  padding: 4px;
  display: flex;
  flex-direction: column;
  min-width: 124px;
}
.role-menu button {
  background: none;
  border: none;
  text-align: left;
  padding: 5px 8px;
  cursor: pointer;
  font-size: 13px;
  border-radius: 4px;
  color: var(--text-dim);
  display: flex;
  align-items: center;
  gap: 6px;
}
.role-menu button:hover {
  background: var(--bg-elev-2);
}
.role-menu .reidentify {
  color: var(--text-faint);
  border-top: 1px solid var(--border);
  margin-top: 2px;
  padding-top: 6px;
}
.svc-port {
  color: var(--card-muted, var(--text-faint));
  flex-shrink: 0;
  font-size: 11.5px;
  min-width: 44px;
}
.svc-url {
  color: var(--accent);
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.svc-url:hover {
  text-decoration: underline;
}
/* 服务未运行时，URL 置灰提示（仍可点击，但访问可能失败） */
.svc-url.dim,
.meta-row.url.dim .v {
  opacity: 0.5;
}
button.dim {
  opacity: 0.5;
}
.meta-row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.meta-row .k {
  color: var(--card-muted, var(--text-faint));
  width: 56px;
  flex-shrink: 0;
}
.meta-row .v {
  color: var(--card-muted, var(--text-dim));
  word-break: break-all;
}
.meta-row.url .v {
  color: var(--accent);
  cursor: pointer;
}
.meta-row.url .v:hover {
  text-decoration: underline;
}
.ellipsis {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  padding-top: 4px;
  border-top: 1px solid var(--card-border, var(--border));
}
.actions button {
  flex-shrink: 0;
}
.danger-ico:hover {
  color: var(--red);
}
/* ===== 卡片背景色选择 ===== */
.color-wrap {
  position: relative;
  display: inline-flex;
}
.color-btn {
  opacity: 0;
  transition: opacity 0.15s;
}
.card:hover .color-btn {
  opacity: 0.7;
}
.color-btn:hover {
  opacity: 1 !important;
}
.color-backdrop {
  position: fixed;
  inset: 0;
  z-index: 5;
}
.color-menu {
  position: absolute;
  right: 0;
  top: 100%;
  z-index: 10;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: var(--shadow);
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 180px;
}
.palette {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 4px;
}
.swatch {
  width: 20px;
  height: 20px;
  border-radius: 4px;
  border: 1px solid var(--border);
  cursor: pointer;
  padding: 0;
}
.swatch.active {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.custom-color {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-dim);
  cursor: pointer;
}
.custom-color input[type='color'] {
  width: 28px;
  height: 20px;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 4px;
  cursor: pointer;
  background: none;
}
.clear-color {
  background: none;
  border: none;
  text-align: center;
  padding: 4px;
  cursor: pointer;
  font-size: 12px;
  border-radius: 4px;
  color: var(--text-faint);
}
.clear-color:hover {
  background: var(--bg-elev-2);
}
</style>
