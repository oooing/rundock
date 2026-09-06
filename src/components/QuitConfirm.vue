<script setup lang="ts">
import { tr } from '@/i18n'

// 通用退出确认弹窗（从托盘「退出」菜单触发）。
// 提示退出会停止所有正在运行的项目服务，需用户二次确认。

const emit = defineEmits<{
  (e: 'confirm'): void
  (e: 'cancel'): void
}>()
</script>

<template>
  <div class="overlay" @click.self="emit('cancel')">
    <div class="modal">
      <header class="m-head">
        <h2>{{ tr("确认退出") }}</h2>
      </header>

      <div class="m-body">
        <p class="msg">{{ tr("退出将停止所有正在运行的项目服务并关闭程序。") }}</p>
        <p class="sub">{{ tr("确定要退出吗？") }}</p>
      </div>

      <footer class="m-foot">
        <button @click="emit('cancel')">{{ tr("取消") }}</button>
        <button class="danger" @click="emit('confirm')">{{ tr("退出") }}</button>
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
  width: 360px;
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
}
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
