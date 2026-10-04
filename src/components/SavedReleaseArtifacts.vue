<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { localBuildApi, LocalBuildError } from '@/api/localBuild'
import { tr } from '@/i18n'
import type { SavedBuildArtifacts } from '@/types/localBuild'
import LocalBuildArtifacts from './LocalBuildArtifacts.vue'

const props = defineProps<{ runId: string }>()
const emit = defineEmits<{ (event: 'sealed', value: boolean): void }>()
const collection = ref<SavedBuildArtifacts | null>(null)
const loading = ref(false)
const error = ref('')
let revision = 0
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
function abort() { revision++; controller?.abort(); if (timer) clearTimeout(timer) }
async function load() {
  abort()
  const current = revision
  const requestController = new AbortController()
  controller = requestController
  timer = setTimeout(() => requestController.abort(), 20000)
  loading.value = true
  error.value = ''
  try {
    const result = await localBuildApi.savedReleaseArtifacts(props.runId, requestController.signal)
    if (current !== revision) return
    collection.value = { artifacts: result.artifacts || [], outputDirectory: result.outputDirectory || '' }
    emit('sealed', collection.value.artifacts.length > 0)
  } catch (reason) {
    if (current !== revision) return
    // Older task records have no sealed output; their legacy metadata remains visible.
    if (reason instanceof LocalBuildError && reason.status === 404) {
      collection.value = null
      emit('sealed', false)
    } else error.value = requestController.signal.aborted ? tr('读取超时，请重试。')
      : reason instanceof Error ? reason.message : tr('操作失败，请重试')
  } finally {
    if (current === revision) { if (timer) clearTimeout(timer); loading.value = false }
  }
}
watch(() => props.runId, () => { collection.value = null; emit('sealed', false); void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(abort)
</script>

<template>
  <div class="saved-release-artifacts">
    <p v-if="loading" class="muted">{{ tr('正在读取已保存产物…') }}</p>
    <div v-if="error" class="saved-artifact-error" role="alert"><p>{{ error }}</p><button type="button" :disabled="loading" @click="load">{{ tr('重新读取产物') }}</button></div>
    <LocalBuildArtifacts v-if="collection?.artifacts.length" :run-id="runId" :artifacts="collection.artifacts" :output-directory="collection.outputDirectory" mode="release" />
  </div>
</template>

<style scoped>
.saved-release-artifacts:empty { display: none; }
.muted { color: var(--text-muted, #b4bdc9); font-size: .875rem; }
.saved-artifact-error { color: #ffa4a4; overflow-wrap: anywhere; padding-block: .7rem; }
button:focus-visible { outline: 2px solid #8dbbff; outline-offset: 3px; }
</style>
