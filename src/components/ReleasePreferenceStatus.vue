<script setup lang="ts">
import { tr } from '@/i18n'
defineProps<{ status: 'idle' | 'saving' | 'saved' | 'error' }>()
</script>

<template>
  <span v-if="status === 'error'" class="preference-save-state" role="status" aria-live="polite">{{ tr('选择未保存') }}</span>
</template>

<style scoped>
.preference-save-state { margin-inline-start: .75rem; color: var(--text-dim); font-size: 11px; font-weight: 400; }
</style>
