<script setup lang="ts">
import { tr } from '@/i18n'

defineProps<{ mode: 'github' | 'local'; disabled: boolean }>()
const emit = defineEmits<{
  (event: 'change', mode: 'github' | 'local'): void
}>()
</script>

<template>
  <fieldset class="build-mode" :disabled="disabled">
    <legend>{{ tr('构建方式') }}</legend>
    <div class="mode-options">
      <div class="mode-card" :class="{ selected: mode === 'github' }">
      <label class="mode-main" for="release-build-cloud">
        <input id="release-build-cloud" type="radio" name="release-build-mode" aria-labelledby="release-build-cloud-title" aria-describedby="release-build-cloud-help" :checked="mode === 'github'" @change="emit('change', 'github')" />
        <span class="option-copy"><span id="release-build-cloud-title">{{ tr('GitHub 云端构建') }}</span><small id="release-build-cloud-help">{{ tr('由 GitHub 构建并发布到 Release，本机不打包。') }}</small></span>
      </label>
      </div>
      <div class="mode-card" :class="{ selected: mode === 'local' }">
      <label class="mode-main" for="release-build-local">
        <input id="release-build-local" type="radio" name="release-build-mode" aria-labelledby="release-build-local-title" aria-describedby="release-build-local-help" :checked="mode === 'local'" @change="emit('change', 'local')" />
        <span class="option-copy"><span id="release-build-local-title">{{ tr('本地构建') }}</span><small id="release-build-local-help">{{ tr('在本机完成构建和打包。') }}</small></span>
      </label>
      <div v-if="mode === 'local'" class="local-build-options"><slot name="local-options" /></div>
      </div>
    </div>
  </fieldset>
</template>

<style scoped>
.build-mode { margin: 0; padding: 0; border: 0; min-width: 0; }
legend { margin-bottom: .5rem; font-size: .9rem; font-weight: 600; }
.mode-options { display: flex; gap: .5rem; flex-wrap: wrap; }
.mode-options { align-items: flex-start; }
.mode-card { flex: 1 1 12rem; min-width: 0; border: 1px solid var(--border); border-radius: .5rem; background: var(--bg); }
.mode-main { display: flex; align-items: center; gap: .5rem; min-height: 2.75rem; padding: .5rem .8rem; cursor: pointer; font-size: .85rem; }
.mode-card.selected { border-color: var(--accent); background: rgba(79,140,255,.1); }
.local-build-options { border-top: 1px solid var(--border); margin: 0 .8rem; padding: .75rem 0; }
input { accent-color: var(--accent); margin: 0; }
.option-copy { display: flex; flex-direction: column; gap: .25rem; min-width: 0; }
.option-copy small { color: var(--text-dim); font-size: 11px; line-height: 1.5; overflow-wrap: anywhere; }
.mode-card:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
fieldset:disabled label { cursor: default; }
fieldset:disabled .mode-card { opacity: .65; }
</style>
