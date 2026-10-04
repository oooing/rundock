<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
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
const deploymentLabels: Record<string, string> = {
  deployment_requested: '正在连接服务器更新任务', deploying: '正在更新服务器',
  deployment_failed: '服务器更新未完成', deployment_rejected: '服务器更新请求被拒绝',
}
// Reconcile while this result is visible; no duplicate requests or background
// retry of a failed deployment. The remote job keeps running after UI closes.
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
onMounted(() => {
  const poll = async () => {
    if (props.deliveries.some(item => item.state === 'published' && ['pending', 'deployment_requested', 'deploying'].includes(item.syncState))) await act('sync')
    if (!disposed) timer = setTimeout(poll, 15000)
  }
  timer = setTimeout(poll, 15000)
})
onUnmounted(() => { disposed = true; clearTimeout(timer); timer = undefined })
const workflowUrl = (message: string) => message.match(/https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/actions\/runs\/\d+/)?.[0] || ''
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
        <div><dt>{{ tr('服务器同步') }}</dt><dd>{{ tr(deploymentLabels[item.syncState] || labels[item.syncState] || item.syncState) }}</dd></div>
      </dl>
      <a v-if="item.url?.startsWith('https://github.com/')" :href="item.url" target="_blank" rel="noopener noreferrer">{{ tr('查看 GitHub Release') }}</a>
      <p v-if="item.errorMessage" role="alert">{{ item.errorMessage }}</p>
      <small v-if="item.syncMessage && item.syncState !== 'unconfigured'">{{ item.syncMessage.replace(workflowUrl(item.syncMessage), '') }}</small>
      <a v-if="workflowUrl(item.syncMessage)" :href="workflowUrl(item.syncMessage)" target="_blank" rel="noopener noreferrer">{{ tr('查看服务器更新') }}</a>
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
