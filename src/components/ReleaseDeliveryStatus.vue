<script setup lang="ts">
import { ref } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { ReleaseRun } from '@/types'
import type { ReleaseDelivery } from '@/types/releaseExecution'
const props = defineProps<{ run: ReleaseRun; deliveries: ReleaseDelivery[] }>()
const emit = defineEmits<{ refresh: [] }>()
const busy = ref(false)
const error = ref('')
const labels: Record<string, string> = {
  sealed: '等待交付', creating_draft: '创建草稿', uploading: '上传中', publishing: '确认正式发布',
  published: '发布成功', failed: '发布待恢复', unconfigured: '未配置验证', unverified: '尚未确认', pending: '等待服务器同步', verified: '服务器版本已核验',
}
async function act(action: 'cancel' | 'sync') {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    if (action === 'cancel') await api.cancelRelease(props.run.id)
    else await api.checkReleaseSync(props.run.id)
    emit('refresh')
  } catch (reason) { error.value = reason instanceof Error ? reason.message : tr('操作失败，请重试') }
  finally { busy.value = false }
}
</script>

<template>
  <section class="delivery-status" aria-live="polite">
    <div v-for="item in deliveries" :key="item.groupId" class="batch">
      <strong>{{ run.versions?.find(version => version.versionGroupId === item.groupId)?.versionGroupName || run.tagName }}</strong>
      <dl>
        <div><dt>{{ tr('构建') }}</dt><dd>{{ tr('成功 · 产物已保存并校验') }}</dd></div>
        <div><dt>{{ tr('发布') }}</dt><dd>{{ tr(labels[item.state] || item.state) }}</dd></div>
        <div><dt>{{ tr('服务器同步') }}</dt><dd>{{ tr(labels[item.syncState] || item.syncState) }}</dd></div>
      </dl>
      <a v-if="item.url?.startsWith('https://github.com/')" :href="item.url" target="_blank" rel="noopener noreferrer">{{ tr('查看 GitHub Release') }}</a>
      <p v-if="item.errorMessage" role="alert">{{ item.errorMessage }}</p>
      <small>{{ tr('产物清单 SHA-256') }}: <code>{{ item.manifestSha256 }}</code></small>
    </div>
    <div class="actions">
      <button v-if="['queued', 'running'].includes(run.status)" :disabled="busy" :aria-busy="busy" @click="act('cancel')">{{ tr(busy ? '正在处理…' : '取消执行') }}</button>
      <button v-if="deliveries.some(item => item.state === 'published' && item.syncState !== 'unconfigured')" :disabled="busy" :aria-busy="busy" @click="act('sync')">{{ tr('重新检查服务器同步') }}</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.batch { border: 1px solid var(--border); border-radius: 8px; padding: 12px; margin: 10px 0; }
dl { display: grid; gap: 8px; margin: 12px 0; } dl > div { display: flex; gap: 12px; } dt { color: var(--text-dim); min-width: 90px; } dd { margin: 0; }
small, code { overflow-wrap: anywhere; color: var(--text-dim); font-size: 11px; }
a { color: var(--accent); display: block; margin-bottom: 8px; } p { color: var(--red, #ef7777); }
.actions { display: flex; gap: 8px; }
button { background: var(--bg); border: 1px solid var(--border); color: var(--text); border-radius: 6px; padding: 8px 12px; cursor: pointer; }
button:disabled { opacity: .6; cursor: wait; }
</style>
