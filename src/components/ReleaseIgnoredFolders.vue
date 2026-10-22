<script setup lang="ts">
import { computed, ref } from 'vue'
import { tr } from '@/i18n'
import type { ReleaseIgnorePolicy } from '@/types'

const props = defineProps<{policy: ReleaseIgnorePolicy; locked?: boolean; update: (id: string, enabled: boolean) => Promise<void>}>()
const busy = ref(''), error = ref(''), notice = ref('')
const enabledCount = computed(() => props.policy.groups.filter(group => group.enabled).length)
const labels = computed(() => ({demos: tr('演示目录'), verification: tr('验证资料'), caches: tr('构建缓存')}))
const hints = computed(() => ({
  demos: tr('项目根目录的 demo、demos。'),
  verification: tr('项目根目录的 artifacts、outputs、reports。'),
  caches: tr('依赖缓存、框架缓存和 Tauri 编译缓存。'),
}))
async function toggle(id: string, enabled: boolean) {
  if (props.locked || busy.value) return
  busy.value = id; error.value = ''; notice.value = ''
  try {
    await props.update(id, enabled)
    notice.value = enabled ? tr('已长期忽略，本地文件保留。') : tr('已撤销本组规则。未被其他规则忽略的文件可重新选择。')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally { busy.value = '' }
}
</script>

<template>
  <details class="managed-ignores">
    <summary>{{tr('长期忽略')}} <span>{{tr('{0} 类',[enabledCount])}}</span></summary>
    <p class="ignore-help">{{tr('已写入 .gitignore，不删除文件，不改变已跟踪文件。')}}</p>
    <div v-for="group in policy.groups" :key="group.id" class="managed-ignore-row">
      <div class="ignore-label"><strong>{{labels[group.id]}}</strong><span>{{hints[group.id]}}</span></div>
      <span class="ignore-state">{{group.enabled ? tr('已忽略') : tr('可选择')}}</span>
      <button type="button" :disabled="locked || !!busy" :aria-label="tr('{0}：{1}',[labels[group.id],group.enabled ? tr('恢复选择') : tr('长期忽略')])" @click="toggle(group.id,!group.enabled)">{{busy===group.id ? tr('正在处理…') : group.enabled ? tr('恢复选择') : tr('长期忽略')}}</button>
    </div>
    <p class="ignore-help">{{tr('恢复只撤销本组规则；你原有的忽略规则仍然生效。')}}</p>
    <p v-if="busy" role="status" class="ignore-help">{{tr('正在更新文件列表，请稍候…')}}</p>
    <p v-if="notice" role="status" class="ignore-help">{{notice}}</p>
    <p v-if="error" role="alert" class="ignore-error">{{error}}</p>
  </details>
</template>

<style scoped>
.managed-ignores { margin: 10px 0; padding: 10px 12px; border: 1px solid var(--border); border-radius: 8px; font-size: 12px; }
summary { cursor: pointer; color: var(--text); }
summary span { margin-left: 6px; color: var(--muted); font-variant-numeric: tabular-nums; }
.ignore-help { margin: 10px 0 0; color: var(--muted); line-height: 1.6; }
.managed-ignore-row { display: flex; align-items: center; gap: 12px; padding: 12px 0; border-bottom: 1px solid var(--border); }
.ignore-label { display: grid; gap: 5px; flex: 1; min-width: 0; }
.ignore-label span, .ignore-state { color: var(--muted); line-height: 1.5; }
.ignore-state { white-space: nowrap; }
button { flex-shrink: 0; min-height: 32px; padding: 5px 10px; }
button:focus-visible,summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.ignore-error { color: var(--danger, #ff8d95); line-height: 1.6; overflow-wrap: anywhere; }
@media (max-width: 480px) { .managed-ignore-row { flex-wrap: wrap; gap: 8px; }.ignore-label { flex-basis: 100%; }button { margin-left: auto; min-height: 40px; } }
</style>
