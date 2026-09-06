<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, onMounted, ref } from 'vue'
import { useGroupsStore } from '@/stores/groups'
import { useAppsStore } from '@/stores/apps'
import UiIcon from '@/components/UiIcon.vue'
import { getAppVersion } from '@/tauri/window'
import pkg from '../../package.json'

const props = defineProps<{ selected: string | null; dropGroupId?: string | null }>()
const emit = defineEmits<{
  (e: 'select', id: string | null): void
  (e: 'settings'): void
  (e: 'help'): void
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
      <span class="logo" aria-hidden="true">⚡</span>
      <div class="brand-text">
        <span class="name">RunDock</span>
        <span class="brand-description">{{ tr('启动坞') }}</span>
      </div>
    </div>

    <nav class="nav" :aria-label="tr('项目分组')">
      <button
        class="nav-item"
        :class="{ active: props.selected === null }"
        :aria-current="props.selected === null ? 'page' : undefined"
        @click="emit('select', null)"
      >
        <UiIcon name="grid" :size="16" />
        <span class="label">{{ tr("全部应用") }}</span>
        <span class="count">{{ countAll }}</span>
      </button>

      <div class="section-title">
        <span>{{ tr("分组") }}</span>
        <button class="ghost icon" :title="tr('新建分组')" :aria-label="tr('新建分组')" @click="newGroup"><UiIcon name="plus" :size="15" /></button>
      </div>

      <button class="nav-item" data-drop-group-id="" :aria-current="props.selected === '' ? 'page' : undefined" :class="{ active: props.selected === '', 'drop-target': props.dropGroupId === '' }" @click="emit('select', '')">
        <UiIcon name="folder" :size="16" /><span class="label">{{ tr('未分组') }}</span>
        <span class="count">{{ apps.apps.filter(a => !a.groupId).length }}</span>
      </button>

      <button
        v-for="g in groups.groups"
        :key="g.id"
        class="nav-item"
        :data-drop-group-id="g.id"
        :title="g.name"
        :aria-current="props.selected === g.id ? 'page' : undefined"
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
        <button class="ghost settings-link" @click="emit('settings')"><UiIcon name="settings" :size="16" />{{ tr('设置') }}</button>
        <button class="ghost icon help-link" :title="tr('使用帮助')" :aria-label="tr('使用帮助')" @click="emit('help')"><UiIcon name="help" :size="17" /></button>
        <span class="version" :title="tr('当前版本')">v{{ appVersion }}</span>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.nav-item.drop-target { outline: 2px solid var(--accent); outline-offset: -2px; background: rgba(79,140,255,.2); }
.sidebar {
  width: 208px;
  flex-shrink: 0;
  background: #15181d;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  padding: 0 12px 16px;
}
.brand {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  flex-shrink: 0;
  min-height: var(--workspace-header-height);
  padding: var(--workspace-header-top) 8px var(--workspace-header-bottom);
}
.brand .logo {
  display: grid;
  place-items: center;
  font-size: 18px;
  flex: 0 0 auto;
  width: 36px;
  height: 36px;
  margin-top: calc((var(--workspace-title-line) + var(--workspace-heading-gap) + var(--workspace-subtitle-line) - 36px) / 2);
}
.brand-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: var(--workspace-heading-gap);
}
.brand .name {
  color: #eef3fa;
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.035em;
  line-height: var(--workspace-title-line);
}
.brand-description {
  color: var(--text-faint);
  font-size: 11px;
  font-weight: 400;
  letter-spacing: 0.06em;
  line-height: var(--workspace-subtitle-line);
  white-space: nowrap;
}
.nav {
  flex: 1;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  padding: 10px;
  border-radius: 8px;
  color: var(--text-dim);
}
.nav-item:hover {
  background: var(--bg-elev-2);
  color: var(--text);
}
.nav-item.active {
  background: rgba(79, 140, 255, 0.15);
  color: #abcaff;
}
.ico {
  width: 16px;
  text-align: center;
}
.ico.swatch {
  width: 12px;
  height: 12px;
  border-radius: 3px;
  margin: 0 2px;
  flex-shrink: 0;
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
  width: 24px;
  flex-shrink: 0;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 26px 11px 6px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-faint);
}
.section-title > button { display: grid; place-items: center; width: 24px; height: 24px; padding: 0; flex-shrink: 0; }
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
.settings-link { display: flex; align-items: center; gap: 10px; }
.footer .help-link { flex: 0 0 auto; display: grid; place-items: center; padding: 5px; }
@media (max-width: 1000px) {
  .sidebar { width: 180px; padding-inline: 8px; }
  .brand { gap: 8px; }
}
@media (max-width: 600px) {
  .sidebar { width: 144px; }
  .brand { padding-inline: 3px; gap: 7px; }
  .brand .logo { width: 30px; height: 30px; margin-top: calc((var(--workspace-title-line) + var(--workspace-heading-gap) + var(--workspace-subtitle-line) - 30px) / 2); }
  .brand .name { font-size: 16px; }
  .brand-description { font-size: 10px; letter-spacing: 0.025em; }
  .nav-item { gap: 7px; padding-inline: 7px; }
  .section-title { padding-inline: 8px; }
  .footer-row { flex-wrap: wrap; }
  .version { padding-left: 12px; }
}
.version {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-faint);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
