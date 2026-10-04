<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { localBuildApi } from '@/api/localBuild'
import { tr } from '@/i18n'
import type { BuildArtifactMode, LocalBuildArtifact } from '@/types/localBuild'

const props = withDefaults(defineProps<{ runId: string; artifacts: LocalBuildArtifact[]; outputDirectory: string; mode?: BuildArtifactMode }>(), { mode: 'local' })
const busy = ref('')
const error = ref('')
const notice = ref('')
const directoryReady = computed(() => props.artifacts.length > 0 && props.artifacts.every(item => item.available))
let revision = 0
let controller: AbortController | undefined
const urls = new Map<string, ReturnType<typeof setTimeout>>()

function reset() {
  revision++
  controller?.abort()
  busy.value = ''
  error.value = ''
  notice.value = ''
}
watch(() => `${props.mode}:${props.runId}`, reset, { flush: 'sync' })
onBeforeUnmount(() => {
  reset()
  for (const [url, timer] of urls) { clearTimeout(timer); URL.revokeObjectURL(url) }
  urls.clear()
})
function size(bytes: number) {
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
}
async function act(artifact?: LocalBuildArtifact) {
  if (busy.value || (artifact && !artifact.available)) return
  const current = ++revision
  const id = props.runId
  const requestController = new AbortController()
  controller = requestController
  const signal = requestController.signal
  const timer = setTimeout(() => requestController.abort(), artifact ? 120000 : 20000)
  busy.value = artifact?.id || 'directory'
  error.value = ''
  notice.value = ''
  try {
    if (artifact) {
      const blob = await localBuildApi.download(id, artifact, signal, props.mode)
      if (current !== revision || signal.aborted) return
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = artifact.name.split(/[\\/]/).pop()?.replace(/[\x00-\x1f<>:"|?*]/g, '_') || 'artifact'
      document.body.append(anchor)
      anchor.click()
      anchor.remove()
      urls.set(url, setTimeout(() => { URL.revokeObjectURL(url); urls.delete(url) }, 30000))
      notice.value = tr('下载已开始')
    } else {
      await localBuildApi.openDirectory(id, signal, props.mode)
      if (current !== revision) return
      notice.value = tr('已打开产物目录')
    }
  } catch (reason) {
    if (current === revision) error.value = signal.aborted ? tr('操作超时，请重试。')
      : artifact && reason instanceof TypeError ? tr('下载失败，请重试；也可在已保存目录中取文件。')
      : reason instanceof Error ? reason.message : tr('操作失败，请重试')
  } finally {
    clearTimeout(timer)
    if (current === revision) busy.value = ''
  }
}
</script>

<template>
  <section class="local-build-artifacts">
    <div class="artifact-heading">
      <h4>{{ tr('构建产物') }}</h4>
      <button v-if="outputDirectory" type="button" :disabled="!!busy || !directoryReady" :aria-busy="busy === 'directory'" @click="act()">{{ busy === 'directory' ? tr('正在打开…') : tr('打开产物目录') }}</button>
    </div>
    <p v-if="outputDirectory" class="saved-path"><span>{{ tr('已保存到') }}</span><code>{{ outputDirectory }}</code></p>
    <p v-if="outputDirectory && !directoryReady" class="muted">{{ tr('有产物不可用，无法打开目录；可用文件仍可下载。') }}</p>
    <ul v-if="artifacts.length" class="artifact-list">
      <li v-for="artifact in artifacts" :key="artifact.id || `${artifact.targetId}-${artifact.name}`">
        <div><strong>{{ artifact.name }}</strong><small>{{ size(artifact.sizeBytes) }}<template v-if="artifact.sha256"> · SHA-256 {{ artifact.sha256.slice(0, 12) }}</template></small><p v-if="!artifact.available" class="artifact-error">{{ tr(artifact.error || '产物不可用，请重新构建。') }}</p></div>
        <button v-if="artifact.available" type="button" :disabled="!!busy" :aria-busy="busy === artifact.id" :aria-label="tr('下载 {0}', [artifact.name])" @click="act(artifact)">{{ busy === artifact.id ? tr('下载中…') : tr('下载') }}</button>
      </li>
    </ul>
    <p v-else class="muted">{{ tr('尚无已保存的产物。') }}</p>
    <p v-if="error" class="artifact-error" role="alert">{{ error }}</p>
    <p class="download-notice" role="status" aria-live="polite">{{ notice }}</p>
  </section>
</template>

<style scoped>
.local-build-artifacts { border-block-start: 1px solid var(--border, #39424e); padding-block-start: .85rem; }
.artifact-heading { display: flex; align-items: center; justify-content: space-between; gap: .75rem; flex-wrap: wrap; }
h4 { margin: 0; font-size: 1rem; }
.saved-path { display: grid; gap: .35rem; margin-block: .7rem; font-size: .875rem; }
code { overflow-wrap: anywhere; color: var(--text, #e5e9ef); }
.artifact-list { list-style: none; padding: 0; margin: 0; }
.artifact-list li { display: flex; align-items: center; justify-content: space-between; gap: .8rem; padding-block: .65rem; border-block-start: 1px solid var(--border, #39424e); }
.artifact-list li > div { min-width: 0; }
strong { display: block; overflow-wrap: anywhere; font-size: .9rem; }
small, .muted, .download-notice { color: var(--text-muted, #b4bdc9); font-size: .8rem; }
.artifact-error { color: #ffa4a4; font-size: .875rem; margin-block: .4rem; overflow-wrap: anywhere; }
.download-notice:empty { display: none; }
button { flex-shrink: 0; min-height: 2.5rem; }
button:focus-visible { outline: 2px solid #8dbbff; outline-offset: 3px; }
@media (pointer: coarse) { button { min-height: 3rem; } }
</style>
