<script setup lang="ts">
import AppCard from '@/components/AppCard.vue'
import type { AppView, ServiceRole } from '@/types'

defineProps<{
  apps: AppView[]
  loading: boolean
  ready: boolean
}>()

const emit = defineEmits<{
  (e: 'start', id: string): void
  (e: 'stop', id: string): void
  (e: 'restart', id: string): void
  (e: 'log', id: string): void
  (e: 'open-url', id: string, url?: string): void
  (e: 'open-dir', id: string): void
  (e: 'delete', id: string): void
  (e: 'import', path: string): void
  (e: 'rename', id: string, name: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void
  (e: 'reidentify', appId: string, serviceId: string): void
}>()
</script>

<template>
  <div class="dashboard">
    <!-- 空状态 -->
    <div v-if="ready && apps.length === 0 && !loading" class="empty">
      <div class="empty-ico">📥</div>
      <h2>拖入脚本即可开始</h2>
      <p class="empty-sub">把任意 <code>.bat</code> / <code>.cmd</code> / <code>.ps1</code> 拖进窗口，或点右上角「＋ 导入脚本」选择文件。</p>

      <div class="guide">
        <div class="guide-row">
          <span class="step">1</span>
          <div class="step-body">
            <div class="step-title">拖入脚本，确认导入</div>
            <div class="step-desc">平台只读分析项目，列出入口、命令、环境变量和<span class="hl">风险项</span>，确认后生成卡片。</div>
          </div>
        </div>
        <div class="guide-row">
          <span class="step">2</span>
          <div class="step-body">
            <div class="step-title">点「启动」，后台无窗口运行</div>
            <div class="step-desc">不弹黑窗。日志实时采集，可点 📜 查看。</div>
          </div>
        </div>
        <div class="guide-row">
          <span class="step">3</span>
          <div class="step-body">
            <div class="step-title">自动发现 URL</div>
            <div class="step-desc">服务起來后，卡片自动显示地址，点 🌐 即用浏览器打开。</div>
          </div>
        </div>
        <div class="guide-row">
          <span class="step">4</span>
          <div class="step-body">
            <div class="step-title">停止 = 连同子进程一起回收</div>
            <div class="step-desc">关掉进程树、释放端口，不留残余。改代码后用「重启」一键再来。</div>
          </div>
        </div>
      </div>

      <div class="tips">
        <span class="tip-k">提示</span>
        首次启动需确认 · 左侧可建分组归类 · 左下角 ⚙ 可导出配置迁移到其它机器
      </div>
    </div>

    <!-- 未就绪 -->
    <div v-else-if="!ready" class="empty">
      <div class="empty-ico spin">⟳</div>
      <h2>正在启动后台服务…</h2>
      <p>sidecar 进程正在初始化，请稍候。</p>
    </div>

    <!-- 卡片网格 -->
    <div v-else class="grid">
      <AppCard
        v-for="a in apps"
        :key="a.id"
        :app="a"
        @start="emit('start', $event)"
        @stop="emit('stop', $event)"
        @restart="emit('restart', $event)"
        @log="emit('log', $event)"
        @open-url="(id, url) => emit('open-url', id, url)"
        @open-dir="emit('open-dir', $event)"
        @delete="emit('delete', $event)"
        @rename="(id, name) => emit('rename', id, name)"
        @set-role="(appId, serviceId, role) => emit('set-role', appId, serviceId, role)"
        @reidentify="(appId, serviceId) => emit('reidentify', appId, serviceId)"
      />
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 16px;
}
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 80px 20px;
  color: var(--text-dim);
}
.empty-ico {
  font-size: 48px;
  margin-bottom: 16px;
  opacity: 0.8;
}
.empty h2 {
  margin: 0 0 8px;
  color: var(--text);
  font-weight: 600;
}
.empty p {
  max-width: 480px;
  line-height: 1.6;
  margin: 0;
}
.empty-sub code {
  background: var(--bg-elev-2);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 12px;
}
.guide {
  margin-top: 28px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 520px;
  text-align: left;
}
.guide-row {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}
.step {
  flex-shrink: 0;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}
.step-body {
  padding-top: 2px;
}
.step-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 2px;
}
.step-desc {
  font-size: 12.5px;
  color: var(--text-dim);
  line-height: 1.55;
}
.hl {
  color: var(--amber);
  font-weight: 600;
}
.tips {
  margin-top: 26px;
  font-size: 12px;
  color: var(--text-faint);
  padding: 10px 14px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 8px;
  max-width: 520px;
  line-height: 1.6;
}
.tip-k {
  color: var(--accent);
  font-weight: 600;
  margin-right: 6px;
}
.spin {
  animation: spin 1.4s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
