<script setup lang="ts">
import { tr } from '@/i18n'

import { computed } from 'vue'
import type { ImportCandidate } from '@/types'

/**
 * mode:
 *   - 'import'（默认）：导入新应用，标题"确认导入此应用"，允许编辑名称
 *   - 'script-change'：启动/重启时脚本内容变化，标题强调"脚本已变更"，只读名称
 */
const props = defineProps<{
  candidate: ImportCandidate
  mode?: 'import' | 'script-change'
  /** script-change 模式下展示的待执行操作文案，如 "启动" / "重启" */
  action?: string
  busy?: boolean
  blocked?: boolean
  error?: string
  cancelLabel?: string
}>()
const emit = defineEmits<{ (e: 'confirm' | 'cancel'): void }>()

const c = computed(() => props.candidate)
const mode = computed(() => props.mode || 'import')
const isScriptChange = computed(() => mode.value === 'script-change')

const hasDanger = computed(
  () => (c.value.findings || []).some((f) => f.level === 'danger')
)
const hasWarn = computed(
  () => (c.value.findings || []).some((f) => f.level === 'warn')
)

const envEntries = computed(() => Object.entries(c.value.env || {}))
const markers = computed(() => c.value.markers || [])

const levelText = (l: string) => ({ danger: tr("危险"), warn: tr("警告"), info: tr("提示") }[l] || l)

const title = computed(() =>
  isScriptChange.value
    ? tr("脚本已变更 — 确认{0}", [props.action || tr("启动")])
    : tr("确认导入此应用"),
)
const hint = computed(() =>
  isScriptChange.value
    ? tr("入口脚本的内容在上次确认后发生了变化。运行脚本等于执行任意代码，请核对新的风险项，确认后再继续。")
    : tr("运行脚本等于执行任意代码。请核对以下信息，确认无误后再导入。这是平台的安全基线。"),
)
const confirmText = computed(() => {
  if (!isScriptChange.value) {
    return hasDanger.value ? tr("我已知晓风险，确认导入") : tr("确认导入")
  }
  return hasDanger.value ? tr("我已知晓风险，确认{0}", [props.action || tr("启动")]) : tr("确认{0}", [props.action || tr("启动")])
})
</script>

<template>
  <div class="overlay">
    <div class="modal" role="dialog" aria-modal="true" :aria-label="title" @keydown.esc="!busy && emit('cancel')">
      <header class="m-head">
        <h2>{{ title }}</h2>
        <button class="ghost icon" :disabled="busy" :aria-label="tr('关闭')" @click="emit('cancel')">✕</button>
      </header>

      <div class="m-body">
        <p v-if="isScriptChange || hasDanger || hasWarn" class="hint">
          {{ hint }}
        </p>

        <section class="block" v-if="!isScriptChange">
          <h4>{{ tr("应用名称") }}</h4>
          <input v-model="c.name" class="name-input" :aria-label="tr('应用名称')" :disabled="busy" />
        </section>

        <section class="block">
          <h4>{{ tr("启动方式") }}</h4>
          <div class="kv"><span>{{ tr("入口脚本") }}</span><code>{{ c.entryScript }}</code></div>
        </section>

        <details class="block" :open="isScriptChange || undefined">
          <summary>{{ tr('高级设置') }}</summary>
          <div class="kv"><span>{{ tr("工作目录") }}</span><code>{{ c.cwd }}</code></div>
          <div class="kv"><span>{{ tr("适配器") }}</span><code>{{ c.adapterType }}</code></div>
          <div class="kv"><span>{{ tr("启动命令") }}</span><code>{{ c.cmd }} {{ c.args.join(' ') }}</code></div>
          <div class="kv" v-if="markers.length">
            <span>{{ tr("项目标志") }}</span><code>{{ markers.join(', ') }}</code></div>
          <div class="kv"><span>{{ tr("端口提示") }}</span><code>{{ c.portHints.join(', ') || tr("无") }}</code></div>
        </details>

        <section class="block" v-if="envEntries.length">
          <h4>{{ tr("将注入的环境变量") }}</h4>
          <div class="env-list">
            <div v-for="[k, v] in envEntries" :key="k" class="env-row">
              <code class="env-k">{{ k }}</code><code class="env-v">{{ v }}</code>
            </div>
          </div>
        </section>

        <section class="block risk" v-if="c.findings && c.findings.length">
          <h4 :class="{ danger: hasDanger, warn: hasWarn && !hasDanger }">{{ tr("风险扫描结果") }}</h4>
          <div
            v-for="(f, i) in c.findings"
            :key="i"
            class="finding"
            :class="f.level"
          >
            <span class="lv">{{ levelText(f.level) }}</span>
            <div class="ft">
              <div class="fm">{{ f.message }}</div>
              <code class="fs" v-if="f.snippet">{{ f.snippet }}</code>
            </div>
          </div>
        </section>
        <p v-if="error" class="import-error" role="alert">{{ error }}</p>
        <p v-if="!isScriptChange" class="muted">{{ tr('添加后生成项目卡片，点击“启动”才会运行。') }}</p>
      </div>

      <footer class="m-foot">
        <button :disabled="busy" @click="emit('cancel')">{{ cancelLabel || tr("取消") }}</button>
        <button
          class="primary"
          :class="{ danger: hasDanger }"
          :disabled="busy || blocked || !c.name.trim()"
          @click="emit('confirm')"
        >
          {{ busy ? tr('正在添加…') : confirmText }}
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
summary { cursor: pointer; color: var(--text-dim); font-size: 13px; margin-bottom: 10px; }
.import-error { color: var(--red); font-size: 13px; }.muted { color: var(--text-dim); font-size: 12px; margin: 0; }
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  padding: 20px;
}
.modal {
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 14px;
  width: 100%;
  max-width: 640px;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
}
.m-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}
.m-head h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}
.m-body {
  padding: 18px 20px;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.hint {
  margin: 0;
  color: var(--text-dim);
  font-size: 13px;
  line-height: 1.6;
  padding: 10px 12px;
  background: rgba(251, 191, 36, 0.08);
  border-left: 3px solid var(--amber);
  border-radius: 4px;
}
.block h4 {
  margin: 0 0 8px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-faint);
}
.block h4.danger {
  color: var(--red);
}
.block h4.warn {
  color: var(--amber);
}
.name-input {
  width: 100%;
}
.kv {
  display: grid;
  grid-template-columns: 90px 1fr;
  gap: 8px;
  padding: 3px 0;
  font-size: 12px;
  align-items: baseline;
}
.kv span {
  color: var(--text-faint);
}
.kv code {
  color: var(--text-dim);
  word-break: break-all;
}
.env-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.env-row {
  display: flex;
  gap: 8px;
  font-size: 12px;
  padding: 3px 6px;
  background: var(--bg);
  border-radius: 4px;
}
.env-k {
  color: var(--purple);
}
.env-v {
  color: var(--text-dim);
  word-break: break-all;
}
.finding {
  display: flex;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 6px;
  margin-bottom: 6px;
  font-size: 12px;
}
.finding.danger {
  background: rgba(248, 113, 113, 0.1);
  border: 1px solid rgba(248, 113, 113, 0.3);
}
.finding.warn {
  background: rgba(251, 191, 36, 0.1);
  border: 1px solid rgba(251, 191, 36, 0.3);
}
.finding.info {
  background: var(--bg);
  border: 1px solid var(--border);
}
.lv {
  font-weight: 600;
  flex-shrink: 0;
  width: 36px;
}
.finding.danger .lv {
  color: var(--red);
}
.finding.warn .lv {
  color: var(--amber);
}
.fm {
  color: var(--text);
  margin-bottom: 3px;
}
.fs {
  color: var(--text-dim);
  background: var(--bg);
  padding: 1px 4px;
  border-radius: 3px;
}
.m-foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
}
button.danger {
  background: var(--red);
  border-color: var(--red);
  color: #fff;
}
</style>
