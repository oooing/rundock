<script setup lang="ts">
import { ref } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
const props = defineProps<{ runId: string; status: string }>()
const emit = defineEmits<{ refresh: [] }>()
const busy = ref(false)
const error = ref('')
async function cancel() {
  if (busy.value || !['queued', 'running'].includes(props.status)) return
  busy.value = true
  error.value = ''
  try {
    await api.cancelRelease(props.runId)
    // The server, not this button, determines the final task state.
    emit('refresh')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : tr('操作失败，请重试')
  } finally { busy.value = false }
}
</script>

<template>
  <div v-if="['queued', 'running'].includes(status)" class="release-cancel">
    <button type="button" :disabled="busy" :aria-busy="busy" @click="cancel">
      <span class="stop-icon" aria-hidden="true"></span>{{ tr(busy ? '正在取消…' : '取消执行') }}
    </button>
    <p v-if="error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.release-cancel { flex-shrink: 0; max-width: 240px; }
button { display: flex; align-items: center; justify-content: center; gap: 9px; min-width: 140px; min-height: 46px; padding: 10px 18px; border: 1px solid #ed858c; border-radius: 10px; background: #552a32; color: #fff1f2; font-size: 14px; font-weight: 650; cursor: pointer; }
button:hover:not(:disabled) { background: #71313d; border-color: #ffb2b8; }
button:focus-visible { outline: 3px solid #ffb2b8; outline-offset: 3px; }
button:disabled { opacity: .75; cursor: wait; }
.stop-icon { width: 11px; height: 11px; border-radius: 2px; background: currentColor; }
p { margin: 8px 0 0; color: #ffb2b8; font-size: 12px; overflow-wrap: anywhere; }
@media (max-width: 520px) { .release-cancel { max-width: none; } button { width: 100%; } }
</style>
