<script setup lang="ts">
import { tr } from '@/i18n'

defineProps<{ current: string; target: string; upgrading: boolean; editable: boolean; label: string }>()
const emit = defineEmits<{ (e: 'change', value: string): void }>()
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
function display(value: string) { return /^(?:v)?\d+\.\d+\.\d+$/.test(value) ? `v${value.replace(/^v/, '')}` : value }
</script>

<template>
  <div class="version-change" :class="{ upgrading }">
    <span v-if="label" class="group-label">{{ label }}</span>
    <div class="version-values">
      <span class="version-end"><small>{{ tr('当前版本') }}</small><strong>{{ display(current) }}</strong></span>
      <template v-if="upgrading">
        <span class="version-arrow" aria-hidden="true">→</span>
        <span class="version-end next-version"><small>{{ tr('升级到') }}</small>
          <input v-if="editable" :value="target" :aria-label="tr('{0}目标版本', [label])" :aria-invalid="!versionPattern.test(target)" spellcheck="false" @input="emit('change', ($event.target as HTMLInputElement).value)" />
          <strong v-else>{{ display(target) }}</strong>
        </span>
      </template>
    </div>
  </div>
</template>

<style scoped>
.version-change { padding: 14px 16px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); min-width: 0; }
.group-label { display: block; margin-bottom: 12px; color: var(--text); font-size: 15px; font-weight: 600; }
.version-values { display: flex; align-items: center; gap: 10px; min-width: 0; }
.version-end { display: flex; flex: 1 1 0; flex-direction: column; gap: 6px; min-width: 0; }
.version-end small { color: var(--text-dim); font-size: 11px; }
.version-end strong { color: var(--text); font-size: 21px; line-height: 1.25; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.next-version strong { color: var(--accent); }
.version-arrow { color: var(--text-faint); margin-top: 20px; font-size: 18px; }
.version-end input { min-width: 0; width: 100%; box-sizing: border-box; font-size: 18px; font-weight: 600; padding: 4px 6px; color: var(--accent); }
.version-end input[aria-invalid="true"] { border-color: var(--red); }
@media(max-width:720px) { .version-values { gap: 6px; }.version-end strong { font-size: 19px; }.version-change { padding: 12px; } }
</style>
