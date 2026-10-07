<script setup lang="ts">
import { tr } from '@/i18n'
defineProps<{ codeOnly?: boolean; embedded?: boolean; policy: 'auto' | 'local'; notice: string; missing: boolean; disabled: boolean }>()
const emit = defineEmits<{ change: [policy: 'auto' | 'local']; settings: [] }>()
</script>

<template>
  <div class="sync-choice" :class="{ embedded }">
    <fieldset :disabled="disabled" :aria-describedby="missing ? 'release-sync-help' : undefined">
      <legend>{{ codeOnly ? tr('提交到哪里') : tr('构建完成后') }}</legend>
      <div class="destination-options">
        <label for="release-sync-github" :class="{ selected: policy === 'auto' }"><input id="release-sync-github" type="radio" name="release-sync-policy" value="auto" :checked="policy === 'auto'" @change="emit('change', 'auto')" /><span>{{ codeOnly ? tr('提交到 GitHub') : tr('自动发布到 GitHub Release') }}</span></label>
        <label for="release-sync-local" :class="{ selected: policy === 'local' }"><input id="release-sync-local" type="radio" name="release-sync-policy" value="local" :checked="policy === 'local'" @change="emit('change', 'local')" /><span>{{ codeOnly ? tr('仅提交到本机') : tr('仅保存在本机') }}</span></label>
      </div>
    </fieldset>
    <p v-if="missing" id="release-sync-help" class="warning" role="alert">{{ notice }} <button type="button" @click="emit('settings')">{{ tr('前往设置') }}</button></p>
  </div>
</template>

<style scoped>
.sync-choice { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem .75rem; padding: .75rem 0; border-bottom: 1px solid var(--border); }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; width: 100%; }
legend { font-size: .85rem; font-weight: 600; margin-bottom: .5rem; }
.destination-options { display: flex; flex-wrap: wrap; gap: .5rem; }
label { display: flex; flex: 1 1 12rem; min-width: 0; overflow-wrap: anywhere; align-items: center; gap: .5rem; font-size: .85rem; min-height: 2.75rem; padding: .4rem .75rem; border: 1px solid var(--border); border-radius: .5rem; background: var(--bg); cursor: pointer; }
label.selected { border-color: var(--accent); background: rgba(79,140,255,.1); }
input { accent-color: var(--accent); margin: 0; }
label:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
fieldset:disabled label { cursor: default; opacity: .65; }
p { flex-basis: 100%; margin: 0; color: var(--text-dim); font-size: .75rem; line-height: 1.5; }
.warning { color: #eab308; }
.sync-choice.embedded { padding: 0; border: 0; }
.embedded legend { font-size: .75rem; color: var(--text-dim); font-weight: 500; }
.embedded .destination-options { flex-direction: column; gap: .25rem; }
.embedded label { flex: none; min-height: 2.25rem; padding: .4rem .5rem; font-size: .8rem; }
</style>
