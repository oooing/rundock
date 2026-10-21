<script setup lang="ts">
import { tr } from '@/i18n'
import type { ImportCandidate } from '@/types'
defineProps<{ candidate: ImportCandidate }>()
</script>

<template>
  <dl class="entry-metadata">
    <dt>{{ tr('入口脚本') }}</dt><dd>{{ candidate.entryScript }}</dd>
    <dt>{{ tr('工作目录') }}</dt><dd>{{ candidate.cwd }}</dd>
    <dt>{{ tr('适配器') }}</dt><dd>{{ candidate.adapterType }}</dd>
    <dt>{{ tr('启动命令') }}</dt><dd>{{ candidate.cmd }} {{ candidate.args.join(' ') }}</dd>
    <dt>{{ tr('端口提示') }}</dt><dd>{{ candidate.portHints.join(', ') || tr('无') }}</dd>
    <template v-if="candidate.markers?.length"><dt>{{ tr('项目标志') }}</dt><dd>{{ candidate.markers.join(', ') }}</dd></template>
    <template v-for="(value, key) in candidate.env" :key="key"><dt>{{ key }}</dt><dd>{{ value }}</dd></template>
  </dl>
</template>

<style scoped>
.entry-metadata { display: grid; grid-template-columns: 80px minmax(0, 1fr); gap: 9px 12px; margin: 14px 0 0; font-size: 12px; line-height: 1.6; }
dt { color: var(--text-dim); overflow-wrap: anywhere; }
dd { margin: 0; overflow-wrap: anywhere; font-family: Consolas, monospace; }
@media(max-width:600px) { .entry-metadata { grid-template-columns: 68px minmax(0, 1fr); gap: 8px; } }
</style>
