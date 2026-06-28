<script setup lang="ts">
import { computed, ref } from 'vue'
import type { AppView, ServiceRole } from '@/types'

const props = defineProps<{ app: AppView }>()
const emit = defineEmits<{
  (e: 'start' | 'stop' | 'restart' | 'log' | 'open-dir' | 'delete', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
}>()

const a = computed(() => props.app)

// 服务角色 → 图标/颜色/标签
const ROLE_META: Record<ServiceRole, { icon: string; color: string; label: string }> = {
  frontend: { icon: '🌐', color: '#3b82f6', label: '前端' },
  backend: { icon: '⚙️', color: '#8b5cf6', label: '后端' },
  database: { icon: '🗄️', color: '#f59e0b', label: '数据库' },
  unknown: { icon: '❓', color: '#9ca3af', label: '未识别' },
}
const ROLE_OPTIONS: ServiceRole[] = ['frontend', 'backend', 'database', 'unknown']

// 当前展开切换菜单的 service id（null = 无）
const roleMenuOpen = ref<string | null>(null)
function roleMeta(role?: string) {
  return ROLE_META[(role as ServiceRole) || 'unknown']
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

const statusLabel = computed(() => {
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

// 打开某个服务的 URL（通过 open-url 事件，后端会用系统浏览器打开）
function openServiceUrl(url: string) {
  emit('open-url', a.value.id, url)
}

const lastStartShort = computed(() => {
  if (!a.value.lastStartedAt) return '—'
  const d = new Date(a.value.lastStartedAt.replace(' ', 'T') + 'Z')
  if (isNaN(d.getTime())) return a.value.lastStartedAt
  return d.toLocaleString('zh-CN', { hour12: false })
})

const adapterLabel = computed(() => {
  const m: Record<string, string> = {
    batch: '批处理',
    ps1: 'PowerShell',
    npm: 'npm',
    yarn: 'yarn',
    pnpm: 'pnpm',
  }
  return m[a.value.adapterType] || a.value.adapterType
})
</script>

<template>
  <article class="card" :class="['s-' + a.status]">
    <header class="head">
      <div class="name-row">
        <span class="adapter-tag">{{ adapterLabel }}</span>
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
        <span class="badge" :class="a.status">
          <span class="dot"></span>{{ statusLabel }}
        </span>
      </div>
    </header>

    <div class="meta">
      <!-- 多服务列表：每个服务一行（角色图标 + 健康点 + 端口 + URL） -->
      <div v-if="a.services && a.services.length" class="services">
        <!-- 切换菜单遮罩：点外部关闭 -->
        <div v-if="roleMenuOpen" class="role-backdrop" @click="roleMenuOpen = null"></div>
        <div v-for="svc in a.services" :key="svc.id" class="svc-row">
          <div class="role-wrap">
            <button
              class="role-btn"
              :class="{ locked: svc.roleSource === 'manual' }"
              :style="{ color: roleMeta(svc.role).color }"
              :title="roleMeta(svc.role).label + (svc.roleSource === 'manual' ? '（已锁定）' : '') + ' — 点击切换'"
              @click.stop="toggleRoleMenu(svc.id)"
            >{{ roleMeta(svc.role).icon }}</button>
            <div v-if="roleMenuOpen === svc.id" class="role-menu" @click.stop>
              <button
                v-for="r in ROLE_OPTIONS"
                :key="r"
                :style="{ color: ROLE_META[r].color }"
                @click="pickRole(svc.id, r)"
              >{{ ROLE_META[r].icon }} {{ ROLE_META[r].label }}</button>
              <button class="reidentify" @click="reidentify(svc.id)">🔄 重新识别</button>
            </div>
          </div>
          <span class="svc-dot" :class="svc.health" :title="healthText(svc.health)"></span>
          <span class="svc-port mono">:{{ svc.port }}</span>
          <a class="svc-url mono" :title="svc.url" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
        </div>
      </div>
      <!-- 无服务时回退到旧的 lastUrl -->
      <div class="meta-row url" v-else-if="a.lastUrl">
        <span class="k">URL</span>
        <a class="v mono" :title="a.lastUrl" @click.prevent="emit('open-url', a.id)">{{ a.lastUrl }}</a>
      </div>
      <div class="meta-row">
        <span class="k">PID</span>
        <span class="v mono">{{ a.pid || '—' }}</span>
      </div>
      <div class="meta-row">
        <span class="k">上次启动</span>
        <span class="v">{{ lastStartShort }}</span>
      </div>
      <div class="meta-row path" :title="a.entryScript">
        <span class="k">入口</span>
        <span class="v mono ellipsis">{{ a.entryScript }}</span>
      </div>
    </div>

    <footer class="actions">
      <template v-if="isActive">
        <button @click="emit('stop', a.id)" class="danger">停止</button>
        <button @click="emit('restart', a.id)">重启</button>
      </template>
      <template v-else>
        <button class="primary" @click="emit('start', a.id)">启动</button>
      </template>
      <button class="ghost icon" title="查看日志" @click="emit('log', a.id)">📜</button>
      <button
        class="ghost icon"
        title="打开 URL"
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
  background: var(--bg-elev);
  border: 1px solid var(--border);
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
.adapter-tag {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--bg-elev-2);
  color: var(--text-dim);
  flex-shrink: 0;
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
  background: var(--bg);
  border-radius: 6px;
  border: 1px solid var(--border);
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
  color: var(--text-faint);
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
.meta-row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.meta-row .k {
  color: var(--text-faint);
  width: 56px;
  flex-shrink: 0;
}
.meta-row .v {
  color: var(--text-dim);
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
  border-top: 1px solid var(--border);
}
.actions button {
  flex-shrink: 0;
}
.danger-ico:hover {
  color: var(--red);
}
</style>
