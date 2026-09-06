<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { useAppsStore } from '@/stores/apps'
import type { LogEntry } from '@/types'

const props = defineProps<{ appId: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const apps = useAppsStore()

const historyLogs = ref<LogEntry[]>([])
const liveLogs = ref<LogEntry[]>([])
const keyword = ref('')
const autoScroll = ref(true)
const loading = ref(false)
const runId = ref('')
const sinceId = ref(0)
const bodyRef = ref<HTMLElement | null>(null)

const app = computed(() => apps.apps.find((a) => a.id === props.appId))
const combined = computed(() => {
  // 合并历史与实时。优先按 id 去重；id 缺失/为 0 时用 ts+stream+text 兜底，避免实时日志互相覆盖。
  const map = new Map<string, LogEntry>()
  const keyOf = (l: LogEntry, fallbackIdx: number) => {
    if (l.id && l.id > 0) return `id:${l.id}`
    return `f:${l.ts}|${l.stream}|${l.text}|${fallbackIdx}`
  }
  let i = 0
  for (const l of historyLogs.value) map.set(keyOf(l, i++), l)
  for (const l of liveLogs.value) map.set(keyOf(l, i++), l)
  return [...map.values()].sort((a, b) => {
    if (a.id && b.id && a.id !== b.id) return a.id - b.id
    return (a.ts || '').localeCompare(b.ts || '')
  })
})

const filtered = computed(() => {
  if (!keyword.value) return combined.value
  const kw = keyword.value.toLowerCase()
  return combined.value.filter((l) => l.text.toLowerCase().includes(kw))
})

async function loadHistory() {
  loading.value = true
  try {
    const r = await api.logs(props.appId, { limit: 500, keyword: keyword.value || undefined })
    runId.value = r.runId
    historyLogs.value = r.logs || []
    if (historyLogs.value.length) {
      sinceId.value = historyLogs.value[historyLogs.value.length - 1].id
    }
  } finally {
    loading.value = false
  }
}

// 订阅 store 的实时日志
watch(
  () => apps.liveLogs[props.appId],
  (arr) => {
    if (arr && arr.length) {
      liveLogs.value = arr
      if (autoScroll.value) nextTick(scrollBottom)
    }
  },
  { deep: true }
)

function scrollBottom() {
  if (bodyRef.value) bodyRef.value.scrollTop = bodyRef.value.scrollHeight
}

function levelTag(level: string) {
  if (level === 'error') return 'ERR'
  if (level === 'warn') return 'WRN'
  if (level === 'debug') return 'DBG'
  return '   '
}

async function clearAndReload() {
  apps.clearLiveLogs(props.appId)
  liveLogs.value = []
  keyword.value = ''
  await loadHistory()
  nextTick(scrollBottom)
}

onMounted(async () => {
  await loadHistory()
  nextTick(scrollBottom)
})
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div class="drawer">
      <header class="d-head">
        <div class="d-title">
          <h3>{{ tr("日志 —") }} {{ app?.name || appId }}</h3>
          <span class="badge" :class="app?.status" v-if="app">
            <span class="dot"></span>{{ app.status }}
          </span>
        </div>
        <div class="d-tools">
          <label class="auto">
            <input type="checkbox" v-model="autoScroll" /> {{ tr("自动滚动") }}
          </label>
          <input
            v-model="keyword"
            :placeholder="tr('搜索 / 过滤')"
            @keyup.enter="loadHistory"
            class="search"
          />
          <button class="ghost icon" @click="clearAndReload" :title="tr('重新加载')">↻</button>
          <button class="ghost icon" @click="emit('close')" :title="tr('关闭')">✕</button>
        </div>
      </header>

      <div class="d-body" ref="bodyRef">
        <div v-if="filtered.length === 0 && !loading" class="no-logs">{{ tr("暂无日志") }}</div>
        <div
          v-for="l in filtered"
          :key="l.id"
          class="log-line"
          :class="[l.stream, l.level]"
        >
          <span class="ts">{{ (l.ts || '').slice(11, 19) || '--:--:--' }}</span>
          <span class="lvl">{{ levelTag(l.level) }}</span>
          <span class="stream" v-if="l.stream === 'event'">EVT</span>
          <span class="txt">{{ l.text }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  justify-content: flex-end;
  z-index: 90;
}
.drawer {
  width: 70%;
  max-width: 900px;
  min-width: 480px;
  background: var(--bg-elev);
  border-left: 1px solid var(--border);
  display: flex;
  flex-direction: column;
}
.d-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  gap: 12px;
}
.d-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.d-title h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}
.d-tools {
  display: flex;
  align-items: center;
  gap: 8px;
}
.auto {
  font-size: 12px;
  color: var(--text-dim);
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}
.search {
  width: 160px;
}
.d-body {
  flex: 1;
  overflow: auto;
  padding: 8px 12px;
  font-family: 'JetBrains Mono', 'Cascadia Code', Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  background: var(--bg);
}
.no-logs {
  color: var(--text-faint);
  text-align: center;
  padding: 40px;
}
.log-line {
  display: flex;
  gap: 8px;
  white-space: pre-wrap;
  word-break: break-all;
  padding: 1px 0;
}
.log-line .ts {
  color: var(--text-faint);
  flex-shrink: 0;
}
.log-line .lvl {
  color: var(--text-faint);
  flex-shrink: 0;
  width: 3ch;
}
.log-line .stream {
  color: var(--purple);
  flex-shrink: 0;
  font-size: 10px;
  opacity: 0.85;
}
.log-line.stderr,
.log-line.error {
  color: var(--red);
}
.log-line.warn {
  color: var(--amber);
}
.log-line.debug .txt {
  color: var(--text-dim);
}
.log-line.event .txt {
  color: var(--purple);
  font-style: italic;
}
</style>
