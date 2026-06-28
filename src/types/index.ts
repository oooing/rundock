// 类型定义，与 Go sidecar 的 JSON tag 对齐。

/** App 状态机 */
export type AppStatus = 'starting' | 'running' | 'degraded' | 'stopping' | 'stopped' | 'failed'

/** 适配器类型 */
export type AdapterType = 'batch' | 'ps1' | 'npm' | 'yarn' | 'pnpm'

/** 风险等级 */
export type RiskLevel = 'info' | 'warn' | 'danger'

/** 一条风险发现 */
export interface Finding {
  level: RiskLevel
  rule: string
  message: string
  snippet: string
}

/** 导入候选（POST /api/import 返回） */
export interface ImportCandidate {
  name: string
  entryScript: string
  cwd: string
  projectRoot: string
  adapterType: AdapterType
  cmd: string
  args: string[]
  env: Record<string, string>
  portHints: number[]
  scriptHash: string
  findings: Finding[] | null
  needsConfirm: boolean
  markers: string[] | null
}

/** App 视图（GET /api/apps 返回，含运行态） */
export interface AppView {
  id: string
  name: string
  entryScript: string
  cwd: string
  adapterType: AdapterType
  cmd: string
  args: string[]
  env: Record<string, string>
  tags: string[]
  groupId?: string | null
  portHints: number[] | null
  healthUrl: string
  scriptHash: string
  confirmed: boolean
  createdAt: string
  lastStartedAt: string | null
  lastUrl: string
  status: AppStatus
  runId: string
  pid: number
  sortOrder: number
  services: AppService[] // 项目下所有服务（前端/后端/DB 各一个端口）
}

/** 创建 App 请求体 */
export interface CreateAppBody {
  name: string
  entryScript: string
  cwd: string
  adapterType: AdapterType
  cmd: string
  args: string[]
  env: Record<string, string>
  tags: string[]
  groupId?: string | null
  portHints: number[]
  healthUrl: string
  scriptHash: string
}

/** 分组 */
export interface Group {
  id: string
  name: string
  color: string
  order: string[]
  createdAt: string
}

/** 日志条目 */
export interface LogEntry {
  id: number
  appRunId: string
  ts: string
  stream: 'stdout' | 'stderr' | 'event'
  level: 'info' | 'warn' | 'error'
  text: string
}

/** 日志历史响应 */
export interface LogsResponse {
  logs: LogEntry[]
  runId: string
}

/** 端口发现记录 */
export interface PortEntry {
  id: number
  appRunId: string
  port: number
  proto: string
  detectedAt: string
}

/** 服务角色 */
export type ServiceRole = 'frontend' | 'backend' | 'database' | 'unknown'

/** 项目下的一个服务（对应一个监听端口） */
export interface AppService {
  id: string
  appId: string
  appRunId: string
  port: number
  url: string
  health: 'healthy' | 'unhealthy' | 'unknown'
  lastChecked: string
  detectedAt: string
  role: ServiceRole
  roleSource: 'auto' | 'manual'
}

/** WebSocket 消息信封 */
export interface WSMessage {
  type: 'app:log' | 'app:event' | 'app:status' | 'app:url' | 'app:services' | 'hello'
  time: string
  appId?: string
  runId?: string
  log?: LogEntry
  event?: { kind: string; port?: number; url?: string; text: string }
  status?: string
  old?: string
  url?: string
  ports?: number[]
  services?: AppService[]
}

/** 事件类型 */
export interface AppEvent {
  kind: 'url_detected' | 'port_listen' | 'ready' | 'build_finished' | 'dependency_waiting'
  port?: number
  url?: string
  text: string
}

/** 配置导出快照 */
export interface ExportSnapshot {
  exportedAt: string
  apps: AppView[]
  groups: Group[]
  settings: Record<string, string>
}
