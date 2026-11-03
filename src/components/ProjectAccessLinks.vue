<script setup lang="ts">
import { ref } from 'vue'
import { tr } from '@/i18n'
import { useAppsStore } from '@/stores/apps'
import type { AccessLink, AppView } from '@/types'
import AccessLinkFields from './AccessLinkFields.vue'
const props = defineProps<{ app: AppView }>()
defineEmits<{ open: [url: string] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const draft = ref<AccessLink[]>([])
const saving = ref(false), error = ref('')
const apps = useAppsStore()
function edit() { draft.value = (props.app.accessLinks || []).map(link => ({ ...link })); error.value = ''; dialog.value?.showModal() }
async function save() {
  if (saving.value) return
  saving.value = true; error.value = ''
  try { await apps.update(props.app.id, { accessLinks: draft.value }); dialog.value?.close() }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { saving.value = false }
}
</script>
<template>
  <div class="access-links">
    <a v-for="link in app.accessLinks" :key="link.url" :href="link.url" :title="link.url" @click.prevent="$emit('open', link.url)">{{ link.name }} ↗</a>
    <button type="button" class="ghost" :aria-label="tr('编辑访问入口')" @click="edit">{{ app.accessLinks?.length ? tr('编辑') : tr('添加访问入口') }}</button>
  </div>
  <Teleport to="body">
    <dialog ref="dialog" class="access-dialog" :aria-label="tr('访问入口')" @cancel="saving && $event.preventDefault()">
      <form @submit.prevent="save">
        <header><h2>{{ app.name }} · {{ tr('访问入口') }}</h2><button type="button" :disabled="saving" :aria-label="tr('关闭')" @click="dialog?.close()">×</button></header>
        <AccessLinkFields v-model="draft" :disabled="saving" />
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <footer><button type="button" :disabled="saving" @click="dialog?.close()">{{ tr('取消') }}</button><button type="submit" class="primary" :disabled="saving">{{ saving ? tr('正在保存…') : tr('保存') }}</button></footer>
      </form>
    </dialog>
  </Teleport>
</template>
<style scoped>
.access-links { display:flex; align-items:center; flex-wrap:wrap; gap:8px 14px; margin:8px 0; font-size:12px; }
.access-links a { color:var(--card-fg,var(--text)); text-decoration:none; border-bottom:1px solid currentColor; padding:4px 0; }
.access-links button { color:var(--card-muted,var(--text-dim)); font-size:11px; padding:4px; }
.access-dialog { width:min(640px,calc(100vw - 32px)); max-height:85vh; overflow:auto; border:1px solid var(--border); border-radius:12px; background:var(--bg-elev); color:var(--text); padding:22px; }
.access-dialog::backdrop { background:#0009; }
header,footer { display:flex; align-items:center; justify-content:space-between; gap:16px; }
h2 { font-size:17px; margin:0; } footer { justify-content:flex-end; margin-top:22px; }.error { color:var(--red); font-size:13px; }
</style>
