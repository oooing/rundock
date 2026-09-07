<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { ReleaseConfig } from '@/types'

const props = defineProps<{ appId: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'saved', config: ReleaseConfig): void; (e: 'editing', value: boolean): void }>()
const file = ref<Awaited<ReturnType<typeof api.getReleaseConfigFile>> | null>(null)
const content = ref('')
const open = ref(false)
const tab = ref<'current' | 'example'>('current')
const busy = ref(false)
const error = ref('')
const saved = ref(false)
const editor = ref<HTMLElement | null>(null)
const dirty = computed(() => file.value !== null && content.value !== file.value.content)

async function show(which: 'current' | 'example') {
  error.value = ''
  if (!open.value) {
    busy.value = true
    try {
      file.value = await api.getReleaseConfigFile(props.appId)
      content.value = file.value.content
      saved.value = false
      open.value = true
      emit('editing', true)
    } catch (reason) {
      error.value = tr('无法打开配置文件：{0}', [reason instanceof Error ? reason.message : String(reason)])
    } finally { busy.value = false }
  }
  tab.value = which
  await nextTick()
  editor.value?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
}

async function save() {
  if (!file.value) return
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    const config = await api.saveReleaseConfigFile(props.appId, content.value, file.value.revision)
    // Saving closes the editor; reopening reads the new disk revision.
    open.value = false
    saved.value = true
    emit('editing', false)
    emit('saved', config)
  } catch (reason) {
    error.value = tr('配置未保存：{0}', [reason instanceof Error ? reason.message : String(reason)])
  } finally { busy.value = false }
}

function close() {
  open.value = false
  error.value = ''
  emit('editing', false)
}

function downloadExample() {
  if (!file.value) return
  const url = URL.createObjectURL(new Blob([file.value.example], { type: 'text/yaml;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'release.example.yaml'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>

<template>
  <div class="config-file-tools">
    <div class="file-toolbar">
      <button :disabled="disabled || busy" @click="show('current')">{{ tr('打开配置文件') }}</button>
      <button :disabled="disabled || busy" @click="show('example')">{{ tr('打开配置样例') }}</button>
      <span v-if="busy" role="status">{{ tr('处理中…') }}</span>
    </div>
    <p v-if="saved" class="file-notice" role="status">{{ tr('配置文件已保存，发布选项已更新。') }}</p>
    <section v-if="open && file" ref="editor" class="file-editor" :aria-label="tr('发布配置文件')">
      <div class="file-toolbar">
        <button :aria-pressed="tab === 'current'" @click="tab = 'current'">{{ tr('当前配置') }}</button>
        <button :aria-pressed="tab === 'example'" @click="tab = 'example'">{{ tr('逐项说明样例') }}</button>
      </div>
      <code class="file-path">{{ tab === 'current' ? file.path : 'release.example.yaml' }}</code>
      <template v-if="tab === 'current'">
        <p v-if="!file.exists" class="file-notice">{{ tr('尚未创建配置文件。这里是自动识别草稿，保存后才会写入项目。') }}</p>
        <p class="file-hint">{{ tr('保存前会校验格式；保存后更新发布选项，不会执行构建或上传。') }}</p>
        <textarea v-model="content" :readonly="busy" :aria-label="tr('配置文件内容')" spellcheck="false" wrap="off"></textarea>
      </template>
      <template v-else>
        <p class="file-hint">{{ tr('样例中的每个字段都有说明。请按实际项目修改；查看或下载样例不会覆盖当前配置。') }}</p>
        <textarea :value="file.example" readonly :aria-label="tr('配置样例内容')" spellcheck="false" wrap="off"></textarea>
      </template>
      <div class="file-toolbar file-footer">
        <span v-if="dirty" class="file-hint">{{ tr('当前配置有未保存的修改') }}</span>
        <button :disabled="busy" @click="close">{{ dirty ? tr('放弃修改并关闭') : tr('关闭文件') }}</button>
        <button v-if="tab === 'example'" :disabled="busy" @click="downloadExample">{{ tr('下载样例文件') }}</button>
        <button v-else class="primary" :disabled="busy || (file.exists && !dirty)" @click="save">{{ tr('校验并保存配置') }}</button>
      </div>
    </section>
    <p v-if="error" class="file-error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.config-file-tools { margin: 12px 0; }
.file-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.file-toolbar button { font-size: 12px; }
.file-toolbar button[aria-pressed="true"] { color: var(--accent); border-color: var(--accent); }
.file-editor { margin-top: 12px; padding: 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--bg); min-width: 0; }
.file-path { display: block; margin: 12px 0; color: var(--text-dim); overflow-wrap: anywhere; font-size: 12px; }
.file-editor textarea { width: 100%; box-sizing: border-box; min-height: 320px; max-height: 55vh; resize: vertical; font-family: Consolas, monospace; font-size: 12px; line-height: 1.7; tab-size: 2; }
.file-hint,.file-notice,.file-error { font-size: 12px; line-height: 1.6; }
.file-hint { color: var(--text-dim); }.file-notice { color: var(--accent); }.file-error { color: var(--danger, #f87171); white-space: pre-wrap; overflow-wrap: anywhere; }
.file-footer { margin-top: 10px; justify-content: flex-end; }
</style>
