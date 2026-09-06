<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, onMounted, ref } from 'vue'
import { useGroupsStore } from '@/stores/groups'
import { useAppsStore } from '@/stores/apps'
import { getAppVersion } from '@/tauri/window'
import pkg from '../../package.json'

const props = defineProps<{ selected: string | null; dropGroupId?: string | null }>()
const emit = defineEmits<{
  (e: 'select', id: string | null): void
  (e: 'settings'): void
}>()

const groups = useGroupsStore()
const apps = useAppsStore()

const countAll = computed(() => apps.apps.length)
const countByGroup = (id: string) => apps.apps.filter((a) => a.groupId === id).length

// 版本号：Tauri 壳里读打包版本，浏览器开发模式回退到 package.json
const appVersion = ref(pkg.version)
onMounted(() => {
  getAppVersion().then((v) => {
    appVersion.value = v
  })
})

async function newGroup() {
  const name = prompt(tr("分组名称"))
  if (!name?.trim()) return
  try { await groups.create(name.trim()) }
  catch (error) { alert(tr('创建分组失败：') + (error instanceof Error ? error.message : String(error))) }
}
</script>

<template>
  <aside class="sidebar">
    <div class="brand">
      <span class="logo">⚡</span>
      <span class="name">{{ tr("RunDock 启动坞") }}</span>
    </div>

    <nav class="nav">
      <button
        class="nav-item"
        :class="{ active: props.selected === null }"
        @click="emit('select', null)"
      >
        <span class="ico">▦</span>
        <span class="label">{{ tr("全部应用") }}</span>
        <span class="count">{{ countAll }}</span>
      </button>

      <div class="section-title">
        <span>{{ tr("分组") }}</span>
        <button class="ghost icon" :title="tr('新建分组')" @click="newGroup">＋</button>
      </div>

      <button class="nav-item" data-drop-group-id="" :class="{ active: props.selected === '', 'drop-target': props.dropGroupId === '' }" @click="emit('select', '')">
        <span class="ico">▢</span><span class="label">{{ tr('未分组') }}</span>
        <span class="count">{{ apps.apps.filter(a => !a.groupId).length }}</span>
      </button>

      <button
        v-for="g in groups.groups"
        :key="g.id"
        class="nav-item"
        :data-drop-group-id="g.id"
        :class="{ active: props.selected === g.id, 'drop-target': props.dropGroupId === g.id }"
        @click="emit('select', g.id)"
      >
        <span class="ico swatch" :style="{ background: g.color || 'var(--text-faint)' }"></span>
        <span class="label">{{ g.name }}</span>
        <span class="count">{{ countByGroup(g.id) }}</span>
      </button>
    </nav>

    <div class="footer">
      <div class="footer-row">
        <button class="ghost" @click="emit('settings')">{{ tr("⚙ 设置") }}</button>
        <span class="version" :title="tr('当前版本')">v{{ appVersion }}</span>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.nav-item.drop-target { outline: 2px solid var(--accent); outline-offset: -2px; background: rgba(79,140,255,.2); }
.sidebar {
  width: 220px;
  flex-shrink: 0;
  background: var(--bg-elev);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  padding: 14px 10px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px 16px;
  font-weight: 600;
  font-size: 15px;
}
.brand .logo {
  font-size: 18px;
}
.nav {
  flex: 1;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  padding: 8px 10px;
  border-radius: 8px;
  color: var(--text-dim);
}
.nav-item:hover {
  background: var(--bg-elev-2);
  color: var(--text);
}
.nav-item.active {
  background: rgba(79, 140, 255, 0.15);
  color: var(--accent);
}
.ico {
  width: 16px;
  text-align: center;
}
.ico.swatch {
  width: 12px;
  height: 12px;
  border-radius: 3px;
  display: inline-block;
}
.label {
  flex: 1;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.count {
  font-size: 11px;
  color: var(--text-faint);
  background: var(--bg-elev-2);
  padding: 1px 7px;
  border-radius: 10px;
}
.section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 10px 6px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-faint);
}
.footer {
  padding-top: 10px;
  border-top: 1px solid var(--border);
}
.footer-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.footer button {
  text-align: left;
  justify-content: flex-start;
  flex: 1;
}
.version {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-faint);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
