<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import type { AppView, StartupIssue } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ app: AppView; locked: boolean; issue: StartupIssue | null; checking: boolean }>()
const emit = defineEmits<{ (event: 'log'): void }>()
const runtimeMessage = computed(() => {
  const message = props.app.runtimeCheck?.message || ''
  // Older backends ask for a manual recheck even though monitoring is automatic.
  return message === '暂时无法确认项目状态，已暂停启动，请稍后重新检查。'
    ? tr('暂时无法确认项目状态，尚未启动项目。状态会自动更新。') : tr(message)
})
const title = computed(() => {
  if (props.checking) return tr('正在检查失败原因…')
  if (props.issue?.conflicts.length) return tr('端口 {0} 被占用', [[...new Set(props.issue.conflicts.map(conflict => conflict.port))].join('、')])
  return tr('启动失败')
})
const description = computed(() => {
  if (props.checking) return ''
  return props.issue?.reason ? tr(props.issue.reason) : tr('打开日志查看失败原因。')
})
</script>

<template>
  <div v-if="locked" class="runtime-notice" role="status">
    <UiIcon :name="app.runtimeCheck?.state === 'running' ? 'server' : 'alert-circle'" :size="15" />
    <div>
      <p>{{ runtimeMessage }}</p>
      <p v-for="conflict in app.runtimeCheck?.conflicts" :key="`${conflict.port}-${conflict.pid}`" class="mono">:{{ conflict.port }} · {{ conflict.name || tr('未知进程') }} · PID {{ conflict.pid }}</p>
      <p v-if="app.runtimeCheck?.reservedPorts?.length" class="mono">{{ tr('系统保留端口') }}: {{ app.runtimeCheck.reservedPorts.join('、') }}</p>
    </div>
  </div>
  <template v-else-if="app.status === 'failed'">
    <button type="button" class="failure-log-link" @click="emit('log')"><UiIcon name="alert-circle" :size="18" /><strong>{{ title }}</strong><span>{{ tr('查看失败日志') }}</span><UiIcon name="arrow-right" :size="14" /></button>
    <details class="startup-error" :aria-busy="checking">
      <summary><strong>{{ tr('原因与处理') }}</strong><UiIcon class="issue-chevron" name="chevron-down" :size="13" /></summary>
      <div class="issue-content">
        <p v-if="description">{{ description }}</p>
        <details v-if="issue?.conflicts.length" class="conflict-details">
          <summary>{{ tr('占用详情') }}<UiIcon name="chevron-down" :size="12" /></summary>
          <p v-for="conflict in issue.conflicts" :key="`${conflict.port}-${conflict.pid}`" class="mono">:{{ conflict.port }} · {{ conflict.name || tr('未知进程') }} · PID {{ conflict.pid }}</p>
        </details>
        <div class="issue-links">
          <button class="ghost" @click="emit('log')">{{ tr('查看日志') }}<UiIcon name="arrow-right" :size="12" /></button>
        </div>
      </div>
    </details>
  </template>
</template>

<style scoped>
.runtime-notice { display: flex; align-items: flex-start; gap: 7px; margin-top: 10px; color: var(--card-muted, var(--text-dim)); font-size: 11px; line-height: 1.6; }
.runtime-notice svg { flex: 0 0 auto; margin-top: 2px; }
.runtime-notice p { margin: 0 0 4px; overflow-wrap: anywhere; }
.failure-log-link { width: 100%; display: flex; align-items: center; gap: 7px; padding: 10px; margin-top: 10px; text-align: left; border: 1px solid var(--card-status-red, var(--red)); border-radius: 7px; background: var(--card-panel, var(--bg)); color: var(--card-status-red, var(--red)); }
.failure-log-link strong { flex: 1; font-size: 12px; }.failure-log-link span { font-size: 11px; white-space: nowrap; }
.failure-log-link:hover { background: var(--card-panel, var(--bg-elev-2)); }
.failure-log-link:focus-visible { outline: 2px solid currentColor; outline-offset: 2px; }
.startup-error { font-size: 12px; line-height: 1.55; overflow-wrap: anywhere; }
.startup-error > summary { display: flex; align-items: center; gap: 7px; padding: 2px 0; border-radius: 3px; cursor: pointer; list-style: none; }
.startup-error > summary::-webkit-details-marker, .conflict-details summary::-webkit-details-marker { display: none; }
.startup-error > summary strong { color: var(--card-fg, var(--text)); font-weight: 500; }
.issue-chevron { margin-left: auto; color: var(--card-muted, var(--text-dim)); }
.startup-error[open] > summary .issue-chevron { transform: rotate(180deg); }
.issue-content { min-width: 0; padding: 4px 0 2px 23px; }.issue-content p { margin: 4px 0 0; color: var(--card-muted, var(--text-dim)); }
.issue-links { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 7px; }
.issue-links > button { display: inline-flex; align-items: center; gap: 5px; padding: 2px 0; border: 0; border-radius: 2px; background: transparent; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.issue-links > button:hover:not(:disabled) { color: var(--card-fg, var(--text)); background: transparent; text-decoration: underline; text-underline-offset: 3px; }
.conflict-details { margin-top: 6px; color: var(--card-muted, var(--text-dim)); font-size: 11px; }
.conflict-details summary { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; list-style: none; }
.conflict-details[open] summary .ui-icon { transform: rotate(180deg); }
</style>
