<script setup lang="ts">
import { tr } from '@/i18n'

defineProps<{ mode: 'github' | 'local'; currentOnly: boolean; disabled: boolean }>()
const emit = defineEmits<{
  (event: 'change', mode: 'github' | 'local'): void
  (event: 'update:currentOnly', value: boolean): void
}>()
</script>

<template>
  <fieldset class="build-mode" :disabled="disabled">
    <legend>{{ tr('构建方式') }}</legend>
    <div class="mode-options">
      <label for="release-build-cloud" :class="{ selected: mode === 'github' }">
        <input id="release-build-cloud" type="radio" name="release-build-mode" :checked="mode === 'github'" @change="emit('change', 'github')" />
        <span>{{ tr('GitHub 云端构建') }}</span>
      </label>
      <label for="release-build-local" :class="{ selected: mode === 'local' }">
        <input id="release-build-local" type="radio" name="release-build-mode" :checked="mode === 'local'" @change="emit('change', 'local')" />
        <span>{{ tr('本地构建') }}</span>
      </label>
    </div>
    <p>{{ mode === 'github' ? tr('由 GitHub 构建和打包，本机不打包。') : tr('在本机生成产物，可保存到本机或发布到 GitHub。') }}</p>
    <label v-if="mode === 'local'" for="release-build-current" class="current-choice">
      <input id="release-build-current" type="checkbox" :checked="currentOnly" @change="emit('update:currentOnly', ($event.target as HTMLInputElement).checked)" />
      <span>{{ tr('仅构建当前版本') }}</span>
    </label>
  </fieldset>
</template>

<style scoped>
.build-mode { margin: 0; padding: 0; border: 0; min-width: 0; }
legend { margin-bottom: .5rem; font-size: .9rem; font-weight: 600; }
.mode-options { display: flex; gap: .5rem; flex-wrap: wrap; }
.mode-options label { display: flex; align-items: center; gap: .5rem; min-height: 2.75rem; flex: 1 1 12rem; padding: .5rem .8rem; border: 1px solid var(--border); border-radius: .5rem; background: var(--bg); cursor: pointer; font-size: .85rem; }
.mode-options label.selected { border-color: var(--accent); background: rgba(79,140,255,.1); }
input { accent-color: var(--accent); margin: 0; }
p { color: var(--text-dim); font-size: .75rem; margin: .5rem 0; line-height: 1.5; }
.current-choice { display: flex; align-items: center; gap: .5rem; min-height: 2.5rem; font-size: .85rem; cursor: pointer; }
label:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
fieldset:disabled label { cursor: default; opacity: .65; }
</style>
