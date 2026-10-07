<script setup lang="ts">
import { nextTick, onMounted, onBeforeUnmount, ref } from 'vue'
import type { Group } from '@/types'
import { tr } from '@/i18n'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ group: Group; rename: (id: string, name: string) => Promise<void>; remove: (id: string) => Promise<void> }>()
const editing = ref(false)
const draft = ref('')
const busy = ref(false)
const error = ref('')
const input = ref<HTMLInputElement | null>(null)
const nameButton = ref<HTMLButtonElement | null>(null)
const menu = ref<HTMLDetailsElement | null>(null)
const trigger = ref<HTMLElement | null>(null)
const dialog = ref<HTMLDialogElement | null>(null)
async function edit() {
  draft.value = props.group.name
  error.value = ''
  editing.value = true
  await nextTick()
  input.value?.focus()
  input.value?.select()
}
async function save() {
  if (!editing.value || busy.value) return
  const name = draft.value.trim()
  if (!name || name === props.group.name) { await cancel(); return }
  busy.value = true
  error.value = ''
  try { await props.rename(props.group.id, name); editing.value = false }
  catch (e: any) { error.value = e?.message || String(e) }
  finally { busy.value = false }
}
async function cancel() {
  editing.value = false
  error.value = ''
  await nextTick()
  nameButton.value?.focus()
}
function onKey(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'Enter') { event.preventDefault(); void save() }
  if (event.key === 'Escape') { event.preventDefault(); void cancel() }
}
function closeMenu(restore = false) {
  if (menu.value) menu.value.open = false
  if (restore) trigger.value?.focus()
}
function outside(event: PointerEvent) {
  if (!menu.value?.contains(event.target as Node)) closeMenu()
}
function confirmDelete() {
  closeMenu()
  error.value = ''
  dialog.value?.showModal()
}
async function remove() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try { await props.remove(props.group.id); dialog.value?.close() }
  catch (e: any) { error.value = e?.message || String(e) }
  finally { busy.value = false }
}
onMounted(() => document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div class="group-heading">
    <div class="heading-row">
      <h1 v-if="!editing"><button ref="nameButton" class="name-trigger" :title="tr('单击修改名称')" @click="edit">{{ group.name }}</button></h1>
      <input v-else ref="input" v-model="draft" class="name-edit" :aria-label="tr('分组名称')" :disabled="busy" @keydown="onKey" @blur="save" />
      <details ref="menu" class="group-menu" @keydown.esc.stop.prevent="closeMenu(true)" @focusout="($event.relatedTarget && !menu?.contains($event.relatedTarget as Node)) && closeMenu()">
        <summary ref="trigger" :aria-label="tr('管理分组')"><UiIcon name="more-vertical" :size="18" /></summary>
        <div class="menu-panel"><button class="delete-action" @click="confirmDelete"><UiIcon name="trash" :size="15" />{{ tr('删除分组') }}</button></div>
      </details>
    </div>
    <p v-if="error && !dialog?.open" class="error" role="alert">{{ error }}</p>
    <dialog ref="dialog" :aria-label="tr('删除分组')" @cancel="busy && $event.preventDefault()" @close="trigger?.focus()">
      <h2>{{ tr('删除分组“{0}”？', [group.name]) }}</h2>
      <p>{{ tr('项目将移到“未分组”，不会被删除或停止。') }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <div class="dialog-actions"><button autofocus :disabled="busy" @click="dialog?.close()">{{ tr('取消') }}</button><button class="delete-action" :disabled="busy" @click="remove">{{ busy ? tr('删除中…') : tr('确认删除') }}</button></div>
    </dialog>
  </div>
</template>

<style scoped>
.heading-row { display: flex; align-items: center; gap: 8px; min-width: 0; }
h1 { margin: 0; min-width: 0; font-size: 24px; line-height: var(--workspace-title-line); font-weight: 600; letter-spacing: -.025em; }
.name-trigger { display: block; max-width: 100%; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; text-align: left; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.name-edit { min-width: 0; width: 240px; max-width: 100%; font-size: 24px; padding: 0 4px; line-height: var(--workspace-title-line); }
.group-menu { position: relative; flex-shrink: 0; }
summary { display: flex; padding: 6px; cursor: pointer; border-radius: 6px; list-style: none; color: var(--text-dim); }
summary::-webkit-details-marker { display: none; }
.menu-panel { position: absolute; top: 100%; left: 0; z-index: 10; min-width: 130px; padding: 5px; background: var(--bg-elev); border: 1px solid var(--border); border-radius: 8px; box-shadow: 0 8px 24px #0006; }
.menu-panel button { width: 100%; display: flex; align-items: center; gap: 8px; border: 0; }
.delete-action, .error { color: var(--red); }
.error { font-size: 12px; overflow-wrap: anywhere; }
dialog { max-width: min(420px, calc(100vw - 32px)); padding: 24px; border: 1px solid var(--border); border-radius: 12px; background: var(--bg-elev); color: var(--text); }
dialog::backdrop { background: #0009; }
dialog h2 { margin: 0 0 12px; font-size: 18px; overflow-wrap: anywhere; }
dialog p { color: var(--text-dim); font-size: 14px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 24px; }
:is(button, input, summary):focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
@media (max-width: 800px) { h1, .name-edit { font-size: 21px; } }
</style>
