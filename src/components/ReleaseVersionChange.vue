<script setup lang="ts">
import { tr } from '@/i18n'

defineProps<{ current: string; target: string; upgrading: boolean; editable: boolean; label: string; compact?: boolean }>()
const emit = defineEmits<{ (e: 'change', value: string): void }>()
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
function display(value: string) { return /^(?:v)?\d+\.\d+\.\d+$/.test(value) ? `v${value.replace(/^v/, '')}` : value }
</script>

<template>
  <div class="version-change" :class="{ upgrading, compact }">
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
.compact { display: flex; align-items: center; justify-content: space-between; gap: 8px 16px; flex-wrap: wrap; padding: 9px 12px; }
.compact .group-label { margin: 0; font-size: 13px; overflow-wrap: anywhere; flex: 1 1 160px; }
.compact .version-values { flex: 0 1 auto; gap: 10px; }
.compact .version-end { flex: none; }
.compact .version-end strong { font-size: 14px; font-weight: 600; }
.compact .version-end small { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
.compact .version-arrow { margin: 0; font-size: 14px; }
.compact .version-end input { width: 108px; min-height: 30px; font-size: 14px; }
.compact .version-end input:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
