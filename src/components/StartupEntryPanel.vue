<script setup lang="ts">
import { tr } from '@/i18n'
import type { ImportCandidate } from '@/types'
import StartupEntryDetails from './StartupEntryDetails.vue'

defineProps<{ candidate: ImportCandidate | null; loading: boolean; error: string; duplicate: boolean; disabled: boolean; collapsible?: boolean }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="entry-panel" :aria-busy="loading">
    <p v-if="loading" class="entry-status" role="status">{{ tr('正在识别…') }}</p>
    <div v-if="error" class="entry-error" role="alert"><span>{{ error }}</span><button :disabled="disabled || loading" @click="$emit('retry')">{{ tr('重试') }}</button></div>
    <template v-if="candidate">
      <div v-if="candidate.findings?.length" class="findings">
        <h3>{{ tr('风险扫描结果') }}</h3>
        <p v-for="(finding, index) in candidate.findings" :key="index" :class="finding.level">{{ finding.message }}<code v-if="finding.snippet">{{ finding.snippet }}</code></p>
      </div>
      <p v-if="duplicate" class="entry-error" role="alert">{{ tr('这个启动入口已经添加，可直接使用现有项目卡片。') }}</p>
      <details v-if="collapsible" class="entry-details">
        <summary>{{ tr('启动详情') }}</summary>
        <StartupEntryDetails :candidate="candidate" />
      </details>
      <StartupEntryDetails v-else :candidate="candidate" />
    </template>
  </div>
</template>

<style scoped>
.entry-status { color: var(--text-dim); font-size: 12px; margin: 12px 0 0; }
.entry-error { color: var(--red); font-size: 13px; line-height: 1.6; margin: 12px 0 0; overflow-wrap: anywhere; }
.entry-error button { margin-left: 8px; }
.entry-details { margin-top: 14px; }
summary { width: fit-content; color: var(--text-dim); font-size: 12px; cursor: pointer; padding: 4px 0; }
summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.findings { margin-top: 14px; }h3 { font-size: 12px; margin: 0; }
.findings p { margin: 8px 0 0; font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.findings code { display: block; }.findings .danger { color: var(--red); }.findings .warn { color: var(--amber); }
</style>
