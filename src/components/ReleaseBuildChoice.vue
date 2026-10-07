<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { tr } from '@/i18n'
import type { ReleaseBuildPlan } from './release/context'
import UiIcon from './UiIcon.vue'

const props = withDefaults(defineProps<{ plan: ReleaseBuildPlan; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ select: [plan: ReleaseBuildPlan] }>()
const open = ref(false)
const picker = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLElement>()
const onlyLocal = computed(() => props.plan === 'local-current' || props.plan === 'local-upgrade')
const localLabel = computed(() => !onlyLocal.value ? '选择版本处理' : props.plan === 'local-current' ? '保持当前版本' : '升级版本')
const directOptions = [
  { id: 'cloud' as const, title: '云端构建并发布', icon: 'github', detail: 'GitHub → Release' },
  { id: 'local-publish' as const, title: '本地构建并发布', icon: 'upload', detail: '本机 → GitHub Release' },
]
const versions = [
  { id: 'local-current' as const, title: '保持当前版本', detail: '版本号不变，只生成安装包', icon: 'square' },
  { id: 'local-upgrade' as const, title: '升级版本', detail: '生成新版本、本地提交和 Tag', icon: 'upload' },
]
async function close(restoreFocus = false) {
  open.value = false
  if (restoreFocus) { await nextTick(); trigger.value?.focus() }
}
async function toggle() {
  if (props.disabled) return
  if (open.value) return close(true)
  open.value = true
  await nextTick()
  const selected = panel.value?.querySelector<HTMLButtonElement>('[aria-pressed="true"]')
  ;(selected || panel.value?.querySelector<HTMLButtonElement>('.version-option'))?.focus()
}
function selectDirect(value: ReleaseBuildPlan) {
  if (props.disabled) return
  void close()
  emit('select', value)
}
function selectVersion(value: ReleaseBuildPlan) {
  if (props.disabled) return
  emit('select', value)
  void close(true)
}
function navigate(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const options = Array.from(panel.value?.querySelectorAll<HTMLButtonElement>('.version-option') || [])
  const current = options.indexOf(event.target as HTMLButtonElement)
  if (current < 0) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1
    : (current + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
  options[next]?.focus()
}
function outside(event: PointerEvent) {
  if (open.value && !picker.value?.contains(event.target as Node)) void close()
}
function leave(event: FocusEvent) {
  if (open.value && event.relatedTarget && !picker.value?.contains(event.relatedTarget as Node)) void close()
}
onMounted(() => document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => document.removeEventListener('pointerdown', outside))
watch(() => props.disabled, disabled => { if (disabled) void close() })
</script>

<template>
  <fieldset class="build-choice" :disabled="disabled">
    <legend>{{ tr('构建与发布') }}</legend>
    <div class="hybrid-cards" role="group" :aria-label="tr('构建与发布方式')">
    <button v-for="option in directOptions" :key="option.id" type="button" class="route-card" :class="{ selected: plan === option.id }" :aria-pressed="plan === option.id" @click="selectDirect(option.id)">
      <span class="card-top"><UiIcon :name="option.icon" :size="21" /><span class="selection-dot"><UiIcon v-if="plan === option.id" name="check" :size="12" /></span></span>
      <strong>{{ tr(option.title) }}</strong><small>{{ tr(option.detail) }}</small>
    </button>

    <div ref="picker" class="local-picker" @focusout="leave" @keydown.esc.stop.prevent="close(true)">
      <button ref="trigger" type="button" class="route-card local-card" :class="{ selected: onlyLocal, expanded: open }" :aria-pressed="onlyLocal" :aria-expanded="open" aria-controls="local-version-cascade" @click="toggle" @keydown.down.prevent="!open && toggle()">
        <span class="card-top"><UiIcon name="folder" :size="21" /><span class="selection-dot"><UiIcon v-if="onlyLocal" name="check" :size="12" /></span></span>
        <strong>{{ tr('仅本地打包') }}</strong>
        <small class="version-result"><span>{{ tr(localLabel) }}</span><UiIcon name="chevron-down" :size="13" :class="{ rotated: open }" /></small>
      </button>

      <div v-if="open" id="local-version-cascade" ref="panel" class="version-popover" role="group" aria-labelledby="local-version-title" @keydown="navigate">
        <header><span id="local-version-title">{{ tr('仅本地打包') }} <span aria-hidden="true">/</span> {{ tr('版本处理') }}</span><button type="button" class="dismiss" :aria-label="tr('取消版本选择')" @click="close(true)"><UiIcon name="close" :size="14" /></button></header>
        <button v-for="option in versions" :key="option.id" type="button" class="version-option" :aria-pressed="plan === option.id" @click="selectVersion(option.id)">
          <UiIcon :name="option.icon" :size="18" /><span><strong>{{ tr(option.title) }}</strong><small>{{ tr(option.detail) }}</small></span><UiIcon v-if="plan === option.id" name="check" :size="14" />
        </button>
        <p><UiIcon name="folder" :size="12" />{{ tr('两种方式都只保存在本机，不上传。') }}</p>
      </div>
    </div>
    </div>
  </fieldset>
</template>

<style scoped>
.build-choice { border: 0; padding: 0; margin: 0; min-width: 0; }
.build-choice > legend { margin-bottom: .5rem; font-size: .9rem; font-weight: 600; }
.build-choice:disabled .route-card { cursor: default; opacity: .65; }
.hybrid-cards { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; align-items: start; }
.route-card { display: block; width: 100%; min-height: 124px; padding: 17px; text-align: left; border: 1px solid #363e4b; border-radius: 9px; background: #14181f; cursor: pointer; color: #edf0f5; }
.route-card:hover { background: #1c2430; border-color: #5b6b83; }
.route-card.selected { background: #202f46; border-color: #7b9fda; }
.route-card.expanded { border-color: #88afea; background: #202a3a; }
.card-top { display: flex; align-items: center; justify-content: space-between; color: #9eb4d4; margin-bottom: 16px; }
.selection-dot { display: grid; place-items: center; width: 16px; height: 16px; border: 1px solid #576275; border-radius: 50%; }
.selected .selection-dot { background: #88afea; border-color: #88afea; color: #15243c; }
.route-card strong { display: block; font-size: 13px; font-weight: 600; line-height: 1.6; }
.route-card small { display: block; font-size: 11px; color: #a7b4c8; line-height: 1.6; margin-top: 4px; }
.route-card .version-result { display: flex; align-items: center; justify-content: space-between; gap: 8px; color: #b6cbed; }
.rotated { transform: rotate(180deg); }
.local-picker { position: relative; min-width: 0; }
.version-popover { position: absolute; z-index: 12; top: calc(100% + 8px); right: 0; width: 100%; min-width: 260px; padding: 6px; background: #202630; border: 1px solid #445066; border-radius: 10px; box-shadow: 0 14px 35px #0008; }
.version-popover header { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 1px 4px 5px 10px; color: #9eacc1; font-size: 10px; }
.version-popover header span span { color: #596a83; margin: 0 4px; }
.dismiss { display: grid; place-items: center; min-width: 30px; min-height: 30px; border: 0; border-radius: 5px; background: transparent; color: #9eacc1; }
.dismiss:hover { background: #303a4b; }
.version-option { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 64px; margin: 2px 0; padding: 10px 11px; border: 0; border-radius: 6px; text-align: left; background: transparent; color: #a9bfde; }
.version-option > span { flex: 1; }.version-option strong { display: block; font-size: 12px; font-weight: 550; color: #e6edf7; line-height: 1.6; }.version-option small { display: block; font-size: 10px; color: #9fadbf; margin-top: 4px; line-height: 1.6; }
.version-option:hover { background: #303a4b; }.version-option[aria-pressed="true"] { background: #2b3c56; }
button:focus-visible { outline: 2px solid #9bbfff; outline-offset: 2px; }
.version-popover p { display: flex; align-items: center; gap: 5px; border-top: 1px solid #354152; padding: 11px 10px 5px; margin: 7px 0 0; font-size: 10px; color: #95a2b6; line-height: 1.6; }
@media (max-width: 760px) { .hybrid-cards { gap: 8px; }.route-card { padding: 13px 11px; } }
@media (max-width: 560px) {
  .hybrid-cards { grid-template-columns: 1fr; }.route-card { display: grid; grid-template-columns: 29px 1fr; column-gap: 12px; min-height: 76px; padding: 15px; }
  .card-top { display: contents; }.card-top > .ui-icon { grid-row: 1 / 3; align-self: center; }.selection-dot { display: none; }.route-card strong, .route-card small { grid-column: 2; }.route-card small { margin-top: 3px; }
  .version-popover { position: relative; top: auto; min-width: 0; margin-top: 8px; }.dismiss { min-height: 44px; min-width: 44px; }
}
</style>
