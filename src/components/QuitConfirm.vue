<script setup lang="ts">
import { tr } from '@/i18n'
import { onMounted, ref } from 'vue'

// 每次完全退出都明确选择；默认焦点在取消，避免误停项目。
const cancelButton = ref<HTMLButtonElement | null>(null)
onMounted(() => cancelButton.value?.focus())

const emit = defineEmits<{
  (e: 'confirm', keepProjects: boolean): void
  (e: 'cancel'): void
}>()
</script>

<template>
  <div class="overlay" @click.self="emit('cancel')" @keydown.esc.stop="emit('cancel')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="quit-title">
      <header class="m-head">
        <h2 id="quit-title">{{ tr("退出软件") }}</h2>
      </header>

      <div class="m-body">
        <p class="msg">{{ tr("如何处理正在运行的项目？") }}</p>
        <button class="quit-option" @click="emit('confirm', true)">
          <strong>{{ tr("保留项目，仅退出软件") }}</strong>
          <span>{{ tr("后台托管继续运行，下次打开可继续管理。") }}</span>
        </button>
        <button class="quit-option stop-projects" @click="emit('confirm', false)">
          <strong>{{ tr("关闭所有项目并退出") }}</strong>
          <span>{{ tr("停止运行中的项目，同时退出后台。") }}</span>
        </button>
      </div>

      <footer class="m-foot">
        <button ref="cancelButton" @click="emit('cancel')">{{ tr("取消") }}</button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  z-index: 110;
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal {
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 14px;
  width: 420px;
  max-width: 92vw;
  display: flex;
  flex-direction: column;
}
.m-head {
  padding: 16px 18px 10px;
  border-bottom: 1px solid var(--border);
}
.m-head h2 {
  margin: 0;
  font-size: 16px;
}
.m-body {
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.quit-option { display: flex; flex-direction: column; gap: 6px; padding: 14px; text-align: left; white-space: normal; background: var(--bg); border: 1px solid var(--border); border-radius: 9px; }
.quit-option span { font-size: 12px; color: var(--text-dim); line-height: 1.5; }
.quit-option:hover { border-color: var(--accent); }
.stop-projects:hover { border-color: var(--red); }
.msg {
  margin: 0 0 6px;
  font-size: 14px;
}
.sub {
  margin: 0;
  font-size: 13px;
  color: var(--text-dim);
}
.m-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px 18px 16px;
}
.m-foot .danger:hover {
  background: var(--red);
  color: #fff;
}
</style>
