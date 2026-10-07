<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import UiIcon from '../UiIcon.vue'
import CardVersionCascade from '../ReleaseBuildChoice.vue'

type Plan = 'cloud' | 'local-publish' | 'local-current' | 'local-upgrade'
type Variant = 'cascade' | 'cards' | 'hybrid'
const initialVariant = new URLSearchParams(location.search).get('variant')
const variant = ref<Variant>(initialVariant === 'cards' || initialVariant === 'hybrid' ? initialVariant : 'cascade')
const variantNumber = computed(() => ({ cascade: '01', cards: '02', hybrid: '03' })[variant.value])
const plan = ref<Plan>('local-publish')
const rememberedLocalPlan = ref<Plan>('local-current')
const open = ref(false)
const picker = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const branch = ref<'cloud' | 'local'>('local')
const destination = ref<'github' | 'local' | null>('github')
const feedback = ref('')
const targets = ref(['pc', 'android', 'web'])
const targetOptions = [
  { id: 'pc', label: 'PC 客户端', detail: 'Windows 桌面端', icon: 'square' },
  { id: 'android', label: 'Android', detail: 'Android 应用', icon: 'grid' },
  { id: 'web', label: 'Web＋服务端', detail: '网站与后台服务', icon: 'server' },
  { id: 'extension', label: '浏览器扩展', detail: '浏览器插件', icon: 'globe' },
]
const cardOptions = [
  { id: 'cloud', title: '云端构建并发布', icon: 'github', detail: 'GitHub → Release' },
  { id: 'local-publish', title: '本地构建并发布', icon: 'upload', detail: '本机 → GitHub Release' },
  { id: 'local', title: '仅本地打包', icon: 'folder', detail: '本机 → 本地文件' },
]
const paths: Record<Plan, string[]> = {
  cloud: ['GitHub 云端构建', '发布到 Release'],
  'local-publish': ['本地构建', '发布到 GitHub Release'],
  'local-current': ['本地构建', '仅保存在本机', '保持当前版本'],
  'local-upgrade': ['本地构建', '仅保存在本机', '升级版本'],
}
const actionLabels: Record<Plan, string> = {
  cloud: '云端构建并发布', 'local-publish': '本地构建并发布',
  'local-current': '仅本地打包', 'local-upgrade': '升级版本并本地打包',
}
const onlyLocal = computed(() => plan.value === 'local-current' || plan.value === 'local-upgrade')
const path = computed(() => paths[plan.value])
const cardValue = computed(() => onlyLocal.value ? 'local' : plan.value)

function changeVariant(value: Variant) {
  variant.value = value
  open.value = false
  feedback.value = ''
}
function setPlan(value: Plan) {
  plan.value = value
  if (value === 'local-current' || value === 'local-upgrade') rememberedLocalPlan.value = value
  feedback.value = ''
}
function chooseCard(value: string) {
  setPlan(value === 'local' ? rememberedLocalPlan.value : value as Plan)
}
async function closePicker(restoreFocus = false) {
  open.value = false
  if (restoreFocus) {
    await nextTick()
    trigger.value?.focus()
  }
}
async function focusColumn(index: number) {
  await nextTick()
  const column = picker.value?.querySelectorAll<HTMLElement>('.cascade-column')[index]
  const button = column?.querySelector<HTMLButtonElement>('[aria-pressed="true"]') || column?.querySelector<HTMLButtonElement>('button')
  button?.focus()
}
async function togglePicker() {
  if (open.value) return closePicker(true)
  branch.value = plan.value === 'cloud' ? 'cloud' : 'local'
  destination.value = onlyLocal.value ? 'local' : plan.value === 'local-publish' ? 'github' : null
  open.value = true
  await focusColumn(0)
}
async function finish(value: Plan) {
  setPlan(value)
  await closePicker(true)
}
async function chooseBranch(value: 'cloud' | 'local') {
  branch.value = value
  destination.value = null
  if (value === 'cloud') return finish('cloud')
  await focusColumn(1)
}
async function chooseDestination(value: 'github' | 'local') {
  destination.value = value
  if (value === 'github') return finish('local-publish')
  await focusColumn(2)
}
function navigateColumn(event: KeyboardEvent, index: number) {
  const element = event.target as HTMLElement
  const buttons = Array.from(element.closest('.cascade-column')?.querySelectorAll<HTMLButtonElement>('button') || [])
  const current = buttons.indexOf(element as HTMLButtonElement)
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault()
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1
      : (current + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length
    buttons[next]?.focus()
  } else if (event.key === 'ArrowLeft' && index > 0) {
    event.preventDefault()
    void focusColumn(index - 1)
  } else if (event.key === 'ArrowRight') {
    event.preventDefault()
    // An explicit key activation advances the selected branch, just like Enter.
    element.click()
  }
}
function outsideClick(event: PointerEvent) {
  if (open.value && !picker.value?.contains(event.target as Node)) void closePicker()
}
function leavePicker(event: FocusEvent) {
  if (event.relatedTarget && !picker.value?.contains(event.relatedTarget as Node)) void closePicker()
}
function previewAction() {
  feedback.value = `已预览：${actionLabels[plan.value]} · ${targets.value.length} 个端。没有创建构建或发布任务。`
}
onMounted(() => document.addEventListener('pointerdown', outsideClick))
onBeforeUnmount(() => document.removeEventListener('pointerdown', outsideClick))
</script>

<template>
  <main class="design-preview">
    <header class="preview-header">
      <a class="brand" href="/" aria-label="回到 RunDock"><span class="brand-mark">R</span>RunDock <span class="brand-divider">/</span><span class="brand-section">交互预览</span></a>
      <span class="preview-badge"><span></span>仅演示，不执行任务</span>
    </header>

    <section class="preview-intro" aria-labelledby="preview-title">
      <p class="eyebrow">BUILD & RELEASE</p>
      <h1 id="preview-title">选择方式，再简单一点。</h1>
      <p>第三种保留三张并列卡片，只将本地打包的版本处理改成级联。原有两版也可以对比。</p>
      <fieldset class="variant-picker">
        <legend class="visually-hidden">视觉方案</legend>
        <label :class="{ active: variant === 'cascade' }"><input type="radio" name="variant" value="cascade" :checked="variant === 'cascade'" @change="changeVariant('cascade')" /><span class="variant-number">01</span><span>第一种 · 级联选择</span></label>
        <label :class="{ active: variant === 'cards' }"><input type="radio" name="variant" value="cards" :checked="variant === 'cards'" @change="changeVariant('cards')" /><span class="variant-number">02</span><span>第二种 · 平铺卡片</span></label>
        <label :class="{ active: variant === 'hybrid' }"><input type="radio" name="variant" value="hybrid" :checked="variant === 'hybrid'" @change="changeVariant('hybrid')" /><span class="variant-number">03</span><span>第三种 · 卡片级联</span></label>
      </fieldset>
    </section>

    <section class="release-preview" aria-labelledby="release-preview-title">
      <header class="release-header">
        <div><span class="app-symbol"><UiIcon name="play" :size="19" /></span><div><h2 id="release-preview-title">发布 localPlay</h2><p>发布设置 · 交互示例</p></div></div>
        <span class="variant-tag">方案 {{ variantNumber }}</span>
      </header>

      <div class="preview-body">
        <section class="build-section" :aria-labelledby="variant === 'hybrid' ? undefined : 'build-title'">
          <div v-if="variant !== 'hybrid'" class="section-heading"><h3 id="build-title">构建与发布</h3><span>{{ variant === 'cascade' ? '逐层选择，完成后收起' : '直接选择一种方式' }}</span></div>

          <div v-if="variant === 'cascade'" ref="picker" class="cascade-picker" @focusout="leavePicker" @keydown.esc.stop.prevent="closePicker(true)">
            <button ref="trigger" type="button" class="cascade-trigger" aria-label="选择构建与发布方式" :aria-expanded="open" aria-controls="build-cascade-panel" @click="togglePicker">
              <UiIcon :name="plan === 'cloud' ? 'github' : 'square'" :size="19" />
              <span class="path-text"><template v-for="(part, index) in path" :key="part"><span v-if="index" class="path-separator" aria-hidden="true">/</span><span>{{ part }}</span></template></span>
              <span class="trigger-action">更改</span><UiIcon name="chevron-down" :size="15" :class="{ rotated: open }" />
            </button>
            <div v-if="open" id="build-cascade-panel" class="cascade-panel" aria-label="逐层选择构建方式">
              <div class="cascade-columns">
                <section class="cascade-column" aria-label="第一层：构建位置" @keydown="navigateColumn($event, 0)">
                  <h4><span>01</span>在哪里构建</h4>
                  <button type="button" :aria-pressed="branch === 'cloud'" @click="chooseBranch('cloud')"><UiIcon name="github" /><span>GitHub 云端构建<small>构建后直接发布 Release</small></span><UiIcon v-if="branch === 'cloud'" name="check" :size="14" /></button>
                  <button type="button" :aria-pressed="branch === 'local'" @click="chooseBranch('local')"><UiIcon name="square" /><span>本地构建<small>使用本机环境打包</small></span><span class="branch-arrow" aria-hidden="true">›</span></button>
                </section>
                <section v-if="branch === 'local'" class="cascade-column" aria-label="第二层：产物去向" @keydown="navigateColumn($event, 1)">
                  <h4><span>02</span>构建完成后</h4>
                  <button type="button" :aria-pressed="destination === 'github'" @click="chooseDestination('github')"><span>发布到 GitHub Release<small>构建成功后自动上传</small></span><UiIcon v-if="destination === 'github'" name="check" :size="14" /></button>
                  <button type="button" :aria-pressed="destination === 'local'" @click="chooseDestination('local')"><span>仅保存在本机<small>不上传 GitHub</small></span><span class="branch-arrow" aria-hidden="true">›</span></button>
                </section>
                <section v-if="branch === 'local' && destination === 'local'" class="cascade-column" aria-label="第三层：版本策略" @keydown="navigateColumn($event, 2)">
                  <h4><span>03</span>版本怎么处理</h4>
                  <button type="button" :aria-pressed="plan === 'local-current'" @click="finish('local-current')"><span>保持当前版本<small>仅生成安装包</small></span><UiIcon v-if="plan === 'local-current'" name="check" :size="14" /></button>
                  <button type="button" :aria-pressed="plan === 'local-upgrade'" @click="finish('local-upgrade')"><span>升级版本<small>本地提交、Tag 和安装包</small></span><UiIcon v-if="plan === 'local-upgrade'" name="check" :size="14" /></button>
                </section>
              </div>
              <div class="cascade-hint"><span>选到最后一级，即完成选择</span><button type="button" @click="closePicker(true)">取消</button></div>
            </div>
          </div>

          <CardVersionCascade v-else-if="variant === 'hybrid'" :plan="plan" @select="setPlan" />
          <template v-else>
            <fieldset class="scenario-cards">
              <legend class="visually-hidden">构建与发布方式</legend>
              <label v-for="option in cardOptions" :key="option.id" :class="{ selected: cardValue === option.id }">
                <input type="radio" name="scenario" :value="option.id" :checked="cardValue === option.id" @change="chooseCard(option.id)" />
                <span class="card-top"><UiIcon :name="option.icon" :size="21" /><span class="selection-dot"><UiIcon v-if="cardValue === option.id" name="check" :size="12" /></span></span>
                <strong>{{ option.title }}</strong><small>{{ option.detail }}</small>
              </label>
            </fieldset>
            <fieldset v-if="onlyLocal" class="version-row">
              <legend>版本处理</legend>
              <label><input type="radio" name="local-version" :checked="plan === 'local-current'" @change="setPlan('local-current')" />保持当前版本</label>
              <label><input type="radio" name="local-version" :checked="plan === 'local-upgrade'" @change="setPlan('local-upgrade')" />升级版本</label>
            </fieldset>
          </template>
          <p class="interaction-note">{{ variant === 'cascade' ? '试试「本地构建 → 仅保存在本机」，可以看到第三层。' : variant === 'hybrid' ? '点击「仅本地打包」选择版本处理，选完收起；再次点击可更改。' : '三种方式直接可见；「仅本地打包」保留独立的版本选择。' }}</p>
        </section>

        <fieldset class="platform-section">
          <legend>选择构建端</legend>
          <div class="platform-options"><label v-for="target in targetOptions" :key="target.id" :class="{ checked: targets.includes(target.id) }"><input v-model="targets" type="checkbox" name="targets" :value="target.id" :aria-label="target.label" /><UiIcon :name="target.icon" :size="21" /><span><strong>{{ target.label }}</strong><small>{{ target.detail }}</small></span><UiIcon v-if="targets.includes(target.id)" name="check" :size="13" /></label></div>
        </fieldset>

        <section class="selection-summary" aria-labelledby="summary-title">
          <h3 id="summary-title">当前选择</h3>
          <p class="result-path" role="status">{{ path.join(' → ') }}</p>
          <div class="summary-meta"><span><UiIcon :name="plan === 'cloud' ? 'github' : 'square'" :size="14" />{{ plan === 'cloud' ? 'GitHub 云端构建' : '本机环境构建' }}</span><span><UiIcon :name="onlyLocal ? 'folder' : 'upload'" :size="14" />{{ onlyLocal ? '不上传' : '发布到 GitHub Release' }}</span><span><UiIcon name="log" :size="14" />{{ plan === 'local-current' ? '版本号不变' : '升级版本' }}</span></div>
        </section>
      </div>

      <footer class="release-footer"><span><UiIcon name="help" :size="15" />示例数据，不读取或修改你的项目</span><button type="button" class="preview-submit" :disabled="!targets.length" @click="previewAction">预览：{{ actionLabels[plan] }}<UiIcon name="arrow-right" :size="16" /></button></footer>
    </section>
    <p v-if="feedback" class="preview-feedback" role="status">{{ feedback }}</p>
    <p class="comparison-caption">{{ variant === 'cascade' ? '01 / 收起时简洁，适合选项多、有多层分支的情况。' : variant === 'hybrid' ? '03 / 保留并列卡片，版本选项按需展开，选完直接显示在卡片里。' : '02 / 所有路径一眼可见，适合当前只有少量组合的情况。' }}</p>
  </main>
</template>

<style scoped>
.design-preview { --preview-border: #30363f; --preview-muted: #a1a9b7; max-width: 1120px; margin: 0 auto; padding: 28px 40px 40px; }
.preview-header, .brand, .preview-badge, .release-header, .release-header > div, .section-heading, .release-footer, .preview-submit, .summary-meta, .summary-meta > span { display: flex; align-items: center; }
.preview-header { justify-content: space-between; gap: 16px; }
.brand { color: #edf0f5; font-weight: 650; text-decoration: none; gap: 12px; }
.brand-mark { display: grid; place-items: center; width: 29px; height: 29px; border: 1px solid #4b6388; border-radius: 8px; background: #213149; color: #afcafd; font-size: 17px; }
.brand-divider { color: #4c5360; font-weight: 400; padding: 0 3px; }.brand-section { font-size: 12px; color: var(--preview-muted); font-weight: 400; }
.preview-badge { gap: 7px; color: #a8b3c3; font-size: 12px; }.preview-badge > span { width: 5px; height: 5px; border-radius: 50%; background: #8598b7; }
.preview-intro { padding: 48px 0 27px; }.eyebrow { color: #8b9fbe; letter-spacing: .2em; font-size: 10px; margin: 0 0 12px; font-weight: 600; }
h1 { font-size: 28px; font-weight: 620; letter-spacing: -.7px; margin: 0 0 12px; }.preview-intro > p:not(.eyebrow) { margin: 0; color: var(--preview-muted); font-size: 13px; line-height: 1.7; }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }button, label { -webkit-tap-highlight-color: transparent; }
.variant-picker { display: inline-flex; padding: 4px; margin-top: 24px; gap: 4px; border-radius: 10px; background: #0c0e12; border: 1px solid #292e37; }
.variant-picker label { display: flex; gap: 9px; align-items: center; min-height: 40px; padding: 8px 19px; cursor: pointer; color: #9da6b5; border-radius: 6px; font-size: 13px; }
.variant-picker label.active { background: #29313e; box-shadow: 0 1px 3px #0005; color: #edf3ff; }.variant-number { font-size: 10px; color: #7d8ca4; }.active .variant-number { color: #b1cdfc; }
.variant-picker input, .scenario-cards input, .platform-options input { position: absolute; width: 1px; height: 1px; clip-path: inset(50%); overflow: hidden; white-space: nowrap; }
label:has(input:focus-visible) { outline: 2px solid #96baff; outline-offset: 3px; }
.release-preview { background: #191d24; border: 1px solid var(--preview-border); border-radius: 14px; box-shadow: 0 18px 65px #0003; }
.release-header { padding: 23px 28px; border-bottom: 1px solid #2b313a; justify-content: space-between; }.release-header > div { gap: 12px; }
.app-symbol { display: grid; place-items: center; width: 38px; height: 38px; background: #26334b; color: #aecbff; border-radius: 11px; }.release-header h2 { margin: 0 0 5px; font-size: 16px; font-weight: 620; }.release-header p { color: var(--preview-muted); font-size: 11px; margin: 0; }.variant-tag { font-size: 11px; color: #9eacbf; background: #222934; padding: 5px 9px; border-radius: 5px; }
.preview-body { padding: 26px 28px; }.section-heading { justify-content: space-between; margin-bottom: 12px; gap: 12px; }h3, .platform-section > legend { font-size: 13px; font-weight: 600; margin: 0; }.section-heading > span { font-size: 11px; color: #909ba9; }
.cascade-picker { position: relative; }.cascade-trigger { display: flex; align-items: center; gap: 12px; width: 100%; min-height: 58px; padding: 13px 17px; border: 1px solid #414b5c; background: #11151b; border-radius: 8px; text-align: start; color: #b4c4dd; }
.cascade-trigger:hover { background: #141a23; }.cascade-trigger[aria-expanded="true"] { border-color: #6898e7; }.path-text { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; flex: 1; font-size: 13px; color: #e0e7f2; line-height: 1.7; }.path-separator { color: #586576; }.trigger-action { font-size: 11px; color: #9caec8; }.rotated { transform: rotate(180deg); }
.cascade-panel { position: absolute; top: calc(100% + 8px); left: 0; width: 100%; z-index: 10; background: #20252e; border: 1px solid #424b59; border-radius: 10px; box-shadow: 0 14px 38px #0008; overflow: hidden; }
.cascade-columns { display: flex; max-height: 48vh; overflow-y: auto; }.cascade-column { flex: 1; min-width: 0; padding: 14px 9px 18px; }.cascade-column + .cascade-column { border-left: 1px solid #373d48; }.cascade-column h4 { display: flex; gap: 8px; margin: 0; padding: 2px 10px 12px; color: #a3adbb; font-size: 11px; font-weight: 500; }.cascade-column h4 > span { color: #758399; font-size: 10px; }
.cascade-column button { display: flex; align-items: center; width: 100%; gap: 9px; background: transparent; border: 0; border-radius: 6px; padding: 11px 10px; text-align: start; min-height: 62px; font-size: 12px; margin-bottom: 3px; }.cascade-column button > span:not(.branch-arrow) { flex: 1; }.cascade-column small { display: block; font-size: 10px; color: #a0adbe; margin-top: 5px; line-height: 1.5; }.cascade-column button[aria-pressed="true"] { color: #c5dcff; background: #2d3e59; }.cascade-column button:hover { background: #303846; }.branch-arrow { font-size: 21px; font-weight: 300; color: #9eacbf; }.cascade-hint { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 17px; border-top: 1px solid #343c47; color: #8f9bae; font-size: 10px; }.cascade-hint button { font-size: 11px; padding: 5px 9px; background: transparent; border: 0; }
.interaction-note { font-size: 11px; color: #8f9aab; line-height: 1.7; margin: 11px 0 0; }
.scenario-cards { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 10px; }.scenario-cards label { cursor: pointer; padding: 17px; min-height: 124px; border: 1px solid #363e4b; border-radius: 9px; background: #14181f; }.scenario-cards label:hover { background: #1c2430; border-color: #5b6b83; }.scenario-cards label.selected { background: #202f46; border-color: #7b9fda; }.card-top { display: flex; align-items: center; justify-content: space-between; color: #9eb4d4; margin-bottom: 16px; }.selection-dot { display: grid; place-items: center; width: 16px; height: 16px; border: 1px solid #576275; border-radius: 50%; }.selected .selection-dot { background: #88afea; border-color: #88afea; color: #15243c; }.scenario-cards strong { display: block; font-size: 13px; font-weight: 600; line-height: 1.6; }.scenario-cards small { display: block; font-size: 11px; color: #a7b4c8; line-height: 1.6; margin-top: 4px; }
.version-row { display: flex; align-items: center; flex-wrap: wrap; column-gap: 22px; row-gap: 8px; margin-top: 14px; padding: 12px 16px; background: #151a21; border-radius: 7px; }.version-row legend { float: left; margin-right: 12px; font-size: 11px; color: #a6b2c4; }.version-row label { display: inline-flex; align-items: center; gap: 7px; cursor: pointer; font-size: 12px; }.version-row input { accent-color: #88afea; margin: 0; width: 14px; height: 14px; }
.platform-section { margin-top: 26px; padding-top: 24px; border-top: 1px solid #2e3540; }.platform-section > legend { float: left; width: 100%; margin: 0 0 13px; }.platform-options { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 9px; clear: both; }.platform-options label { display: flex; align-items: center; gap: 9px; min-height: 70px; padding: 13px 12px; border: 1px solid #323c4b; border-radius: 7px; background: #14191f; color: #a2b6d6; cursor: pointer; }.platform-options label.checked { background: #1b2738; border-color: #4f668a; }.platform-options label > span { flex: 1; }.platform-options strong { display: block; color: #dce3ed; font-size: 11px; line-height: 1.7; font-weight: 600; }.platform-options small { display: block; color: #9eabbe; font-size: 10px; line-height: 1.7; }
.selection-summary { border-top: 1px solid #2e3540; padding-top: 24px; margin-top: 26px; }.selection-summary h3 { color: #9ba8ba; font-size: 11px; font-weight: 500; }.result-path { font-size: 14px; line-height: 1.8; margin: 10px 0 14px; color: #dfe9f9; }.summary-meta { gap: 24px; flex-wrap: wrap; row-gap: 10px; }.summary-meta > span { gap: 6px; font-size: 11px; color: #a3b0c3; }
.release-footer { justify-content: space-between; gap: 16px; padding: 19px 28px; border-top: 1px solid #2e3540; }.release-footer > span { display: flex; align-items: center; gap: 6px; color: #8f9bac; font-size: 11px; }.preview-submit { gap: 12px; min-height: 42px; border-radius: 7px; background: #386ac4; border-color: #5782cc; font-size: 12px; padding: 9px 15px; }.preview-submit:hover:not(:disabled) { background: #4377d3; }.comparison-caption { text-align: center; font-size: 11px; color: #8f9aac; margin: 21px 0 0; line-height: 1.7; }.preview-feedback { text-align: center; padding: 12px; color: #bcd2f4; font-size: 12px; background: #1b2a40; border-radius: 8px; }
.visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; clip-path: inset(50%); white-space: nowrap; overflow: hidden; }
@media (max-width: 560px) { .variant-picker { flex-wrap: wrap; }.variant-picker label { flex-basis: 100%; } }
@media (max-width: 760px) { .design-preview { padding: 22px 20px 30px; }.preview-intro { padding-top: 32px; }.platform-options { grid-template-columns: repeat(2,minmax(0,1fr)); }.scenario-cards { gap: 8px; }.scenario-cards label { padding: 13px 11px; }.release-footer { align-items: flex-start; flex-direction: column; }.preview-submit { align-self: flex-end; }.cascade-column { padding: 11px 5px; }.cascade-column button { padding: 10px 7px; } }
@media (max-width: 560px) { .design-preview { padding: 18px 12px 24px; }.brand-section, .brand-divider { display: none; }.preview-badge { font-size: 10px; }h1 { font-size: 23px; }.preview-intro > p:not(.eyebrow) { font-size: 12px; }.variant-picker { display: flex; }.variant-picker label { flex: 1; padding: 9px 10px; font-size: 12px; justify-content: center; gap: 7px; }.release-header, .release-footer { padding: 18px; }.preview-body { padding: 20px 18px; }.section-heading { align-items: flex-start; flex-direction: column; gap: 6px; }.cascade-trigger { padding: 12px; gap: 8px; }.path-text { font-size: 12px; gap: 6px; }.trigger-action { display: none; }.cascade-panel { position: relative; top: auto; margin-top: 8px; }.cascade-columns { flex-direction: column; max-height: none; }.cascade-column + .cascade-column { border-left: 0; border-top: 1px solid #373d48; }.cascade-column button { min-height: 58px; font-size: 13px; }.cascade-column small { font-size: 11px; }.scenario-cards { grid-template-columns: 1fr; }.scenario-cards label { display: grid; grid-template-columns: 29px 1fr; column-gap: 12px; min-height: 76px; padding: 15px; }.card-top { display: contents; }.card-top > .ui-icon { grid-row: 1 / 3; align-self: center; }.selection-dot { display: none; }.scenario-cards strong { grid-column: 2; }.scenario-cards small { grid-column: 2; margin-top: 3px; }.version-row { gap: 14px; }.version-row legend { width: 100%; margin-bottom: 5px; }.summary-meta { flex-direction: column; align-items: flex-start; }.platform-options label { padding: 12px 9px; gap: 7px; }.release-preview { border-radius: 11px; } }
</style>
