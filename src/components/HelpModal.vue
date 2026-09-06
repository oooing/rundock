<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, ref } from 'vue'

const emit = defineEmits<{ (e: 'close'): void }>()

// 左侧菜单
const tabs = computed(() => ([
  { id: 'quick', label: tr("快速上手") },
  { id: 'status', label: tr("状态与操作") },
  { id: 'strategy', label: tr("智能策略") },
  { id: 'tips', label: tr("小提示") },
] as const))
type TabId = (typeof tabs.value)[number]['id']
const activeTab = ref<TabId>('quick')

const statuses = computed(() => ([
  { cls: 'starting', name: tr("启动中"), desc: tr("刚点启动，正在初始化（装依赖、编译、起服务）。健康检查会持续复查，就绪后自动转运行中。") },
  { cls: 'running', name: tr("运行中"), desc: tr("进程活着，健康检查通过，URL 可正常访问。") },
  { cls: 'degraded', name: tr("降级"), desc: tr("进程活着，但健康检查暂未通过。常见原因：服务还在编译、刚起来还没就绪、或 /health 端点不存在。每 5 秒自动复查一次，恢复后转运行中。") },
  { cls: 'stopping', name: tr("停止中"), desc: tr("正在优雅停止（Ctrl-Break → 等待 → 强制回收进程树）。") },
  { cls: 'stopped', name: tr("已停止"), desc: tr("正常停止，端口已释放。") },
  { cls: 'failed', name: tr("失败"), desc: tr("进程异常退出（崩溃/报错）。看日志排查。") },
]))

const ops = computed(() => ([
  { key: tr("启动 / 停止 / 重启"), desc: tr("控制应用运行") },
  { key: '📜', desc: tr("查看实时日志") },
  { key: '🌐', desc: tr("用浏览器打开发现的 URL") },
  { key: '📁', desc: tr("打开项目目录") },
  { key: '🗑', desc: tr("删除应用") },
  { key: tr("✎ / 双击名称"), desc: tr("改名") },
]))

const strategies = computed(() => ([
  {
    title: tr("多服务自动识别"),
    desc: tr("一个项目内可能有前端、后端、数据库多个服务，各自监听不同端口。平台会自动发现它们，每个端口独立显示健康状态，点端口行可直接打开对应服务。"),
  },
  {
    title: tr("双证据端口归属"),
    desc: tr("判断\"这个端口属于哪个项目\"用两条证据：① 端口的进程在本项目的进程树内（强证据）；② 项目日志里提到了这个端口的 URL（补证据，覆盖进程树断裂场景）。两条证据都不满足的端口不会被收入，所以多个项目同时运行也不会把对方的端口算进来。"),
  },
  {
    title: tr("启动前自动清端口"),
    desc: tr("启动项目时，若检测到该项目历史用过的端口被残留进程占用，会自动杀掉占用进程，然后才启动。告别 EADDRINUSE 端口冲突。"),
  },
  {
    title: tr("时间窗约束"),
    desc: tr("日志证据必须叠加时间窗：端口必须是\"本项目启动之后\"才开始监听的。启动前就存在的端口不会被误收，避免把无关端口或别的项目早就占的端口算进来。"),
  },
  {
    title: tr("木桶原则综合状态"),
    desc: tr("项目整体状态由所有服务综合判定：全部健康 = 运行中；任一不健康 = 降级；全挂 = 失败。一个服务出问题不会静默，会反映到项目状态上。"),
  },
  {
    title: tr("持续健康复查（低开销）"),
    desc: tr("降级状态的服务每 5 秒复查一次，恢复就转运行中并停止复查。运行中的服务不再复查，零开销。复查只对 127.0.0.1 发本地 HTTP 请求，不走网络。"),
  },
]))

const tips = computed(() => ([
  tr("导入：粘贴完整路径（含盘符），或点「浏览」选文件后补全路径"),
  tr("改名：双击卡片名称，或悬停后点 ✎"),
  tr("改代码后：前端自动热更新；后端需重启 dev.bat 重新编译"),
  tr("关窗口/关服务：正在运行的应用会停止，配置永久保留在数据库"),
  tr("数据位置：%APPDATA%\\launcher-sidecar\\launcher.db"),
]))
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div class="modal">
      <header class="m-head">
        <h2>{{ tr("使用说明") }}</h2>
        <button class="ghost icon" @click="emit('close')">✕</button>
      </header>

      <div class="m-body">
        <!-- 左侧菜单 -->
        <nav class="side-menu">
          <button
            v-for="t in tabs"
            :key="t.id"
            class="menu-item"
            :class="{ active: activeTab === t.id }"
            @click="activeTab = t.id"
          >
            {{ t.label }}
          </button>
        </nav>

        <!-- 右侧内容 -->
        <div class="content">
          <!-- 快速上手 -->
          <div v-show="activeTab === 'quick'" class="pane">
            <div class="quick-step">
              <span class="step-num">1</span>
              <div>
                <div class="step-t">{{ tr("导入脚本") }}</div>
                <div class="step-d">{{ tr("在顶栏输入框粘贴 start.bat 完整路径（含盘符），回车或点导入。确认卡核对信息后生成卡片。") }}</div>
              </div>
            </div>
            <div class="quick-step">
              <span class="step-num">2</span>
              <div>
                <div class="step-t">{{ tr("点「启动」") }}</div>
                <div class="step-d">{{ tr("项目在后台无窗口运行。日志实时采集，点 📜 查看。") }}</div>
              </div>
            </div>
            <div class="quick-step">
              <span class="step-num">3</span>
              <div>
                <div class="step-t">{{ tr("查看服务与 URL") }}</div>
                <div class="step-d">{{ tr("卡片自动列出项目内所有服务端口，点端口行用浏览器打开对应地址。") }}</div>
              </div>
            </div>
            <div class="quick-step">
              <span class="step-num">4</span>
              <div>
                <div class="step-t">{{ tr("停止 / 重启") }}</div>
                <div class="step-d">{{ tr("停止会连同子进程一起回收、释放端口。改代码后用「重启」一键再来。") }}</div>
              </div>
            </div>
          </div>

          <!-- 状态与操作 -->
          <div v-show="activeTab === 'status'" class="pane">
            <h4 class="pane-title">{{ tr("状态含义") }}</h4>
            <div class="status-list">
              <div v-for="s in statuses" :key="s.cls" class="status-row">
                <span class="badge" :class="s.cls"><span class="dot"></span>{{ s.name }}</span>
                <span class="status-desc">{{ s.desc }}</span>
              </div>
            </div>
            <h4 class="pane-title">{{ tr("卡片操作") }}</h4>
            <div class="op-grid">
              <div v-for="o in ops" :key="o.key" class="op">
                <span class="op-key">{{ o.key }}</span>
                <span class="op-desc">{{ o.desc }}</span>
              </div>
            </div>
          </div>

          <!-- 智能策略 -->
          <div v-show="activeTab === 'strategy'" class="pane">
            <div class="feature-list">
              <div v-for="f in strategies" :key="f.title" class="feature-row">
                <span class="feature-title">{{ f.title }}</span>
                <span class="feature-desc">{{ f.desc }}</span>
              </div>
            </div>
          </div>

          <!-- 小提示 -->
          <div v-show="activeTab === 'tips'" class="pane">
            <ul class="tips">
              <li v-for="(t, i) in tips" :key="i">{{ t }}</li>
            </ul>
          </div>
        </div>
      </div>

      <footer class="m-foot">
        <span class="kbd-hint">{{ tr("按") }} <kbd>?</kbd> {{ tr("或") }} <kbd>Esc</kbd> {{ tr("关闭") }}</span>
      </footer>
    </div>
  </div>
</template>

<style scoped>
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
  width: 780px;
  height: 580px;
  max-width: calc(100vw - 40px);
  max-height: calc(100vh - 40px);
  display: flex;
  flex-direction: column;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.5);
}
.m-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 24px;
  border-bottom: 1px solid var(--border);
}
.m-head h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
}
/* 主体：左侧菜单 + 右侧内容 */
.m-body {
  flex: 1;
  display: flex;
  overflow: hidden;
}
.side-menu {
  width: 150px;
  flex-shrink: 0;
  border-right: 1px solid var(--border);
  padding: 16px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  overflow-y: auto;
}
.menu-item {
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  padding: 9px 12px;
  border-radius: 8px;
  color: var(--text-dim);
  font-size: 13.5px;
  white-space: nowrap;
}
.menu-item:hover {
  background: var(--bg-elev-2);
  color: var(--text);
}
.menu-item.active {
  background: rgba(79, 140, 255, 0.15);
  color: var(--accent);
  font-weight: 500;
}
.content {
  flex: 1;
  overflow: auto;
  padding: 22px 26px;
}
.pane {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
/* 快速上手 */
.quick-step {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}
.step-num {
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
.step-t {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 3px;
}
.step-d {
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.6;
}
/* 标题 */
.pane-title {
  margin: 0 0 14px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-faint);
  font-weight: 600;
}
/* 状态 */
.status-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.status-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}
.status-row .badge {
  flex-shrink: 0;
  white-space: nowrap;
  min-width: 72px;
  justify-content: center;
}
.status-desc {
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.6;
  padding-top: 3px;
  flex: 1;
}
/* 操作 */
.op-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 24px;
}
.op {
  font-size: 13px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.op-key {
  color: var(--text);
  font-weight: 500;
  white-space: nowrap;
  background: var(--bg-elev-2);
  padding: 3px 10px;
  border-radius: 6px;
  min-width: 92px;
  text-align: center;
}
.op-desc {
  color: var(--text-dim);
}
/* 智能策略 */
.feature-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.feature-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
}
.feature-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--accent);
}
.feature-desc {
  font-size: 12.5px;
  color: var(--text-dim);
  line-height: 1.65;
}
/* 小提示 */
.tips {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.9;
}
.m-foot {
  padding: 14px 24px;
  border-top: 1px solid var(--border);
  text-align: center;
}
.kbd-hint {
  font-size: 11px;
  color: var(--text-faint);
}
kbd {
  background: var(--bg-elev-2);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 1px 6px;
  font-size: 11px;
  font-family: monospace;
}
</style>
