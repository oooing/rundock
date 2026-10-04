<script setup lang="ts">
import { tr } from '@/i18n'
defineProps<{ policy: 'auto' | 'local'; notice: string; repository: string; missing: boolean; disabled: boolean; status: 'idle' | 'saving' | 'saved' | 'error' }>()
const emit = defineEmits<{ change: [policy: 'auto' | 'local']; settings: [] }>()
</script>

<template>
  <div class="sync-choice">
    <label for="release-sync-policy">{{ tr('发布到哪里') }}</label>
    <select id="release-sync-policy" :value="policy" :disabled="disabled" @change="emit('change', ($event.target as HTMLSelectElement).value as 'auto' | 'local')">
      <option value="auto">{{ tr('GitHub（自动识别）') }}</option>
      <option value="local">{{ tr('仅保存在本机') }}</option>
    </select>
    <span class="save-state" role="status" aria-live="polite">{{ status === 'saving' ? tr('正在保存…') : status === 'saved' ? tr('已自动保存') : '' }}</span>
    <p :class="{ warning: missing }">{{ notice }} <button v-if="missing" type="button" @click="emit('settings')">{{ tr('前往设置') }}</button></p>
    <code v-if="policy === 'auto' && repository">{{ repository.replace('https://github.com/', '') }}</code>
  </div>
</template>

<style scoped>
.sync-choice { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem .75rem; padding: .75rem 0; border-bottom: 1px solid var(--border); }
label { font-size: .85rem; font-weight: 600; }
select { min-height: 2.75rem; padding: .4rem .6rem; max-width: 100%; border: 1px solid var(--border); border-radius: .5rem; background: var(--bg); color: var(--text); }
select:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
p { flex-basis: 100%; margin: 0; color: var(--text-dim); font-size: .75rem; line-height: 1.5; }
code, .save-state { color: var(--text-dim); font-size: .75rem; overflow-wrap: anywhere; }
.warning { color: #eab308; }
</style>
