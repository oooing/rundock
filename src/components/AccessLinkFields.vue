<script setup lang="ts">
import { tr } from '@/i18n'
import type { AccessLink } from '@/types'
const props = defineProps<{ modelValue?: AccessLink[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [links: AccessLink[]] }>()
function change(index: number, key: keyof AccessLink, value: string) {
  emit('update:modelValue', (props.modelValue || []).map((link, i) => i === index ? { ...link, [key]: value } : link))
}
function remove(index: number) { emit('update:modelValue', (props.modelValue || []).filter((_, i) => i !== index)) }
</script>
<template>
  <div class="access-fields">
    <div v-for="(link, index) in modelValue" :key="index" class="access-row">
      <input :value="link.name" :aria-label="tr('入口名称 {0}', [index + 1])" :placeholder="tr('名称')" :disabled="disabled" required @input="change(index, 'name', ($event.target as HTMLInputElement).value)" />
      <input :value="link.url" :aria-label="tr('入口地址 {0}', [index + 1])" placeholder="http://localhost:3000/admin" type="url" :disabled="disabled" required @input="change(index, 'url', ($event.target as HTMLInputElement).value)" />
      <button type="button" :disabled="disabled" :aria-label="tr('移除入口 {0}', [index + 1])" @click="remove(index)">×</button>
    </div>
    <button type="button" class="ghost add-link" :disabled="disabled" @click="emit('update:modelValue', [...(modelValue || []), { name: '', url: '' }])">+ {{ tr('添加访问入口') }}</button>
  </div>
</template>
<style scoped>
.access-fields { display:grid; gap:10px; margin-top:10px; }
.access-row { display:grid; grid-template-columns:minmax(75px, .6fr) minmax(110px, 1.6fr) 32px; gap:8px; }
.access-row input { width:100%; min-width:0; }
.add-link { justify-self:start; }
</style>
