<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { tr } from '@/i18n'
const props = defineProps<{ text: string }>()
const state = ref<'idle' | 'copying' | 'copied' | 'failed'>('idle')
const manual = ref<HTMLTextAreaElement | null>(null)
let revision = 0
let timer: ReturnType<typeof setTimeout> | undefined
function reset() { revision++; if (timer) clearTimeout(timer); state.value = 'idle' }
watch(() => props.text, reset)
onBeforeUnmount(reset)
async function copy() {
  if (!props.text || state.value === 'copying') return
  if (timer) clearTimeout(timer)
  const current = ++revision
  state.value = 'copying'
  try {
    if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
    await navigator.clipboard.writeText(props.text)
    if (current !== revision) return
    state.value = 'copied'
    timer = setTimeout(() => { state.value = 'idle' }, 2000)
  } catch {
    if (current !== revision) return
    state.value = 'failed'
    await nextTick()
    if (current === revision) { manual.value?.focus(); manual.value?.select() }
  }
}
</script>

<template>
  <div class="copy-error-control">
    <button type="button" :disabled="!text || state === 'copying'" :aria-busy="state === 'copying'" :aria-label="tr('复制错误信息')" @click="copy"><span role="status" aria-live="polite">{{ state === 'copied' ? tr('已复制') : state === 'copying' ? tr('复制中…') : tr('复制错误信息') }}</span></button>
    <div v-if="state === 'failed'" class="copy-error-fallback">
      <p role="alert">{{ tr('未能自动复制，请选中下方内容手动复制。') }}</p>
      <textarea ref="manual" :value="text" readonly rows="5" :aria-label="tr('错误信息（可手动复制）')" @focus="manual?.select()"></textarea>
    </div>
  </div>
</template>

<style scoped>
.copy-error-control { max-width: 100%; min-width: 0; }
.copy-error-fallback { margin-top: 8px; }
.copy-error-fallback p { color: var(--amber); font-size: 12px; margin: 0 0 6px; }
.copy-error-fallback textarea { box-sizing: border-box; width: 100%; min-width: 0; resize: vertical; font-size: 12px; }
</style>
