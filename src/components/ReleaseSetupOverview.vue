<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { ReleaseConfig } from '@/types'
import { getReleaseSetupSummary } from '@/utils/releaseSetup'

const props = defineProps<{ appId: string; config: ReleaseConfig | null; disabled: boolean; available: boolean }>()
const emit = defineEmits<{ saved: [config: ReleaseConfig]; advanced: []; busy: [value: boolean] }>()
const draft = ref<ReleaseConfig | null>(null)
const busy = ref<'scan' | 'save' | ''>('')
const error = ref('')
const notice = ref('')
const config = computed(() => draft.value || props.config)
const rows = computed(() => getReleaseSetupSummary(config.value))
const saved = computed(() => props.config?.source === 'file')
const locked = computed(() => props.disabled || !!busy.value)
watch(() => props.config, () => { draft.value = null })

async function scan() {
  if (locked.value || saved.value) return
  busy.value = 'scan'; emit('busy', true); error.value = ''; notice.value = ''
  try { draft.value = await api.scanReleaseConfig(props.appId) }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { busy.value = ''; emit('busy', false) }
}
async function save() {
  if (locked.value || saved.value || !config.value?.targets.length) return
  busy.value = 'save'; emit('busy', true); error.value = ''; notice.value = ''
  try {
    const { schemaVersion, versionGroups, targets, automation, fileRules, checkProfiles } = config.value
    // First-time setup only. An existing or concurrently created file must never
    // be replaced by heuristic detection (it may contain custom signing/delivery).
    const result = await api.saveReleaseConfigFile(props.appId,
      JSON.stringify({ schemaVersion, versionGroups, targets, automation, fileRules, checkProfiles }, null, 2), 'missing')
    emit('saved', result)
    notice.value = tr('已保存。可到“发布”选择构建方式和发布端。')
  } catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason) }
  finally { busy.value = ''; emit('busy', false) }
}
function stateLabel(state: string) {
  return state === 'configured' ? tr('已配置') : state === 'disabled' ? tr('已停用') : state === 'incomplete' ? tr('需补充') : tr('未配置')
}
</script>

<template>
  <section class="setup-overview" :aria-label="tr('项目发布设置')" :aria-busy="!!busy">
    <header><h3>{{ tr('可发布的端') }}</h3><span class="saved-state">{{ saved ? tr('已读取项目配置') : tr('自动识别结果') }}</span></header>
    <p class="intro">{{ saved ? tr('日常发布只需选择构建方式和发布端，这里通常不用改。') : tr('已自动查找项目的构建方式，保存后即可重复使用。') }}</p>
    <ul v-if="rows.length" class="setup-list">
      <li v-for="row in rows" :key="row.id" class="setup-row">
        <strong>{{ row.name }}</strong>
        <div class="build-methods">
          <div v-for="method in (['cloud', 'local'] as const)" :key="method" class="method" :class="row[method].state">
            <span>{{ method === 'cloud' ? tr('GitHub 云端构建') : tr('本地构建') }}</span><span class="method-state">{{ stateLabel(row[method].state) }}</span>
            <small v-if="row[method].state !== 'configured'">{{ row[method].reason }}</small>
          </div>
        </div>
      </li>
    </ul>
    <p v-else class="empty">{{ available ? tr('暂未找到构建方式，试试自动识别。') : tr('暂时无法读取项目配置，请稍后重试。') }}</p>
    <p v-if="rows.some(row => row.local.state !== 'configured' || row.cloud.state !== 'configured')" class="hint">{{ tr('可使用已配置的方式；需要其他方式时再补充设置。') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <div class="setup-actions">
      <template v-if="!saved && available">
        <button type="button" :disabled="locked" @click="scan">{{ busy === 'scan' ? tr('识别中…') : tr('自动识别') }}</button>
        <button v-if="rows.length" type="button" class="primary" :disabled="locked" @click="save">{{ busy === 'save' ? tr('正在保存…') : tr('保存并使用') }}</button>
      </template>
      <button type="button" class="advanced-link" :disabled="locked" @click="emit('advanced')">{{ tr('补充或调整设置') }}</button>
    </div>
    <details v-if="config?.warnings.length" class="setup-notes"><summary>{{ tr('识别说明') }}</summary><p v-for="warning in config.warnings" :key="warning">{{ warning }}</p></details>
  </section>
</template>

<style scoped>
.setup-overview { padding: 18px; border: 1px solid var(--border); border-radius: 10px; background: var(--bg); }
header { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
h3 { margin: 0; font-size: 15px; }
.saved-state, .intro, .hint, .empty, .setup-notes { color: var(--text-dim); font-size: 12px; line-height: 1.6; }
.intro { margin: 8px 0 14px; }
.setup-list { list-style: none; padding: 0; margin: 0; }
.setup-row + .setup-row { border-top: 1px solid var(--border); margin-top: 12px; padding-top: 12px; }
.setup-row > strong { display: block; margin-bottom: 8px; font-size: 14px; overflow-wrap: anywhere; }
.build-methods { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.method { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 8px; padding: 9px 10px; border-radius: 6px; background: var(--bg-elev); font-size: 12px; }
.method-state { margin-inline-start: auto; color: var(--text-dim); }
.configured .method-state { color: var(--green); }
.incomplete .method-state { color: var(--amber); }
.method small { flex-basis: 100%; color: var(--text-faint); line-height: 1.5; overflow-wrap: anywhere; }
.setup-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
.advanced-link { margin-inline-start: auto; }
.error { color: var(--red); font-size: 13px; }
.notice { color: var(--green); font-size: 13px; }
.setup-notes { margin-top: 12px; }
.setup-notes summary { cursor: pointer; }
@media (max-width: 560px) { .build-methods { grid-template-columns: 1fr; } }
</style>
