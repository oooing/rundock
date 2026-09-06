<script setup lang="ts">
import { tr } from '@/i18n'

// 关闭窗口选择弹窗：点 X 关闭时弹出，让用户选「最小化到托盘」或「退出程序」。
// 选项由父组件(App.vue)处理具体动作（隐藏窗口/退出 + 记忆）。
// 非 Tauri 环境（开发模式）下父组件不会触发此弹窗。

const emit = defineEmits<{
  (e: 'minimize'): void
  (e: 'quit'): void
  (e: 'close'): void // 点遮罩/取消（什么也不做，窗口保持打开）
}>()
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div class="modal close-modal">
      <header class="m-head">
        <h2>{{ tr("关闭窗口") }}</h2>
        <button class="ghost icon" @click="emit('close')" :title="tr('取消')">✕</button>
      </header>

      <div class="m-body">
        <p class="hint">{{ tr("关闭窗口后，如何处理？") }}</p>
        <div class="options">
          <button class="opt" @click="emit('minimize')">
            <span class="opt-ico">🗕</span>
            <span class="opt-text">
              <span class="opt-title">{{ tr("最小化到托盘") }}</span>
              <span class="opt-desc">{{ tr("后台保持运行，记住选择（下次不再询问）") }}</span>
            </span>
          </button>
          <button class="opt danger" @click="emit('quit')">
            <span class="opt-ico">⏻</span>
            <span class="opt-text">
              <span class="opt-title">{{ tr("退出程序") }}</span>
              <span class="opt-desc">{{ tr("选择关闭项目或保留运行") }}</span>
            </span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal {
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 14px;
  max-height: 88vh;
  width: 420px;
  max-width: 92vw;
  display: flex;
  flex-direction: column;
}
.m-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 18px 10px;
  border-bottom: 1px solid var(--border);
}
.m-head h2 {
  margin: 0;
  font-size: 16px;
}
.m-body {
  padding: 16px 18px 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.hint {
  margin: 0;
  color: var(--text-dim);
  font-size: 13px;
}
.options {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.opt {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.15s, background 0.15s;
}
.opt:hover {
  border-color: var(--accent);
}
.opt.danger:hover {
  border-color: var(--red);
}
.opt-ico {
  font-size: 22px;
  flex-shrink: 0;
  width: 32px;
  text-align: center;
}
.opt-text {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.opt-title {
  font-size: 14px;
  font-weight: 600;
}
.opt-desc {
  font-size: 12px;
  color: var(--text-faint);
}
</style>
