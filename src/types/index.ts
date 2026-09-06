// 类型定义，与 Go sidecar 的 JSON tag 对齐。

/** App 状态机 */
export type AppStatus = 'starting' | 'running' | 'degraded' | 'stopping' | 'stopped' | 'failed'

export interface StartupIssue {
  code: 'startup_failed' | 'port_in_use'
  runId: string
  ports: number[]
  conflicts: { port: number; pid: number; name: string; safe: boolean }[]
  canRecover: boolean
  reason: string
  fingerprint: string
}

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
  /** 仅用于前端交互：重启请求及启动恢复期间保持为 true */
  restarting?: boolean
  runId: string
  pid: number
  sortOrder: number
  services: AppService[] // 项目下所有服务（前端/后端/DB 各一个端口）
  cardColor: string
}

/** 启动/重启接口的成功响应体 */
export interface StartResponse {
  started?: boolean
  restarted?: boolean
  /** true 表示后端在启动前自动同步了脚本派生字段，附带最新 app */
  configUpdated?: boolean
  app?: AppView
}

/** 启动/重启时返回 409 的响应体（脚本风险变化需确认） */
export interface ScriptConfirmationResponse {
  /** 固定 "script_confirmation_required"，前端据此识别 */
  code: 'script_confirmation_required'
  /** 最新的导入候选（含 findings），用 ConfirmCard 展示 */
  candidate: ImportCandidate
}

/** 启动/重启待执行操作的类型 */
export type PendingOp = 'start' | 'restart'

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
  cardColor: string
  sortOrder: number
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
  level: 'info' | 'warn' | 'error' | 'debug'
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

export type VersionStrategy = 'auto' | 'manual' | 'node' | 'tauri'

/** 发布版本生成方式：自动使用建议版本，或由操作人手动指定。 */
export type ReleaseVersionMode = 'auto' | 'manual'

/** 一次发布中对单个目标实际执行的阶段。 */
export interface SelectedReleaseTarget {
  targetId: string
  build: boolean
  package: boolean
  publish: boolean
  deploy: boolean
}

export interface ReleaseProfile {
  appId: string
  remoteName: string
  versionStrategy: VersionStrategy
  preReleaseCommand: string
  /** 是否为发布提交创建 annotated Tag；服务端会按项目记忆。 */
  createTag: boolean
  /** 自动递增或手动输入目标版本；服务端会按项目记忆。 */
  versionMode: ReleaseVersionMode
  updatedAt?: string
}

/** `.launcher/release.yaml` 中的一条版本文件映射；Cargo.lock 使用 cargo-lock 格式。 */
export interface ReleaseVersionFile {
  path: string
  format: string
  jsonPointer?: string
}

/** 共用同一个版本号的一组发布目标。 */
export interface ReleaseVersionGroup {
  id: string
  name: string
  /** 多版本项目的 Tag 命名空间，例如 web 会生成 web/v2.1.0。 */
  tagPrefix?: string
  currentVersion?: string
  versionFiles: ReleaseVersionFile[]
}

export interface ReleaseTargetRunner {
  type: string
  os: string[]
}

/** 一个目标可配置的通用命令；空字符串表示没有该阶段。 */
export interface ReleaseTargetSteps {
  check?: string
  build?: string
  package?: string
  publish?: string
  deploy?: string
}

/** 发布目标并不限定平台；kind 可由项目使用任意标识。 */
export interface ReleaseTarget {
  id: string
  name: string
  kind: string
  versionGroup: string
  workingDir: string
  runner: ReleaseTargetRunner
  enabled: boolean
  detected: boolean
  confidence: number
  steps: ReleaseTargetSteps
  artifacts: string[]
}

/** Tag 推送后由外部平台接管的自动发布流程。 */
export interface ReleaseAutomation {
  provider: string
  workflow: string
  trigger: string
  releaseBranch: string
  publishesRelease: boolean
}

/**
 * 通用发布配置。普通用户通过 UI 管理，后端将其保存到
 * `.launcher/release.yaml`；source/repoRoot/configPath 是只读来源信息。
 */
export interface ReleaseConfig {
  schemaVersion: number
  source?: 'file' | 'detected'
  repoRoot?: string
  configPath?: string
  confidence: number
  versionGroups: ReleaseVersionGroup[]
  targets: ReleaseTarget[]
  automation?: ReleaseAutomation
  warnings: string[]
}

export interface ReleaseIssue {
  code: string
  message: string
}

export interface ReleaseFileChange {
  path: string
  status: string
  tracked: boolean
  staged: boolean
}

export interface ReleaseCommittedFileChange {
  path: string
  status: string
}

export interface ReleasePreflight {
  repoRoot: string
  branch: string
  headSha: string
  remoteName: string
  remoteUrl: string
  remotes: string[]
  latestTag: string
  latestGroupTags: Record<string, string>
  suggestedVersion: string
  suggestedVersions: Record<string, string>
  versionStrategy: Exclude<VersionStrategy, 'auto'>
  versionFiles: string[]
  currentVersions: Record<string, string>
  changes: ReleaseFileChange[]
  aheadCount: number
  unpushedChanges: ReleaseCommittedFileChange[]
  blockingIssues: ReleaseIssue[]
  canRelease: boolean
  /** 是否已经完成远程分支同步检查；发布前必须为 true。 */
  remoteChecked: boolean
  statusFingerprint: string
  profile: ReleaseProfile
}

export interface ReleaseNotesDraftRequest {
  statusFingerprint: string
  selectedPaths: string[]
  selectedTargets: SelectedReleaseTarget[]
}

export interface ReleaseNotesDraft {
  text: string
  baseTag: string
  commitCount: number
  changeCount: number
  sourceFingerprint: string
}

export type ReleaseStage =
  | 'preparing' | 'versioning' | 'checking' | 'committing' | 'tagging'
  | 'building_targets' | 'publishing_targets'
  | 'target_check' | 'target_build' | 'target_package' | 'target_publish' | 'target_deploy'
  | 'pushing_branch' | 'pushing_tag' | 'completed'

export interface ReleaseRun {
  id: string
  appId: string
  repoRoot: string
  branch: string
  remoteName: string
  targetVersion: string
  tagName: string
  createTag: boolean
  pushRemote: boolean
  versions?: ReleaseVersion[]
  selectedTargets: SelectedReleaseTarget[]
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  stage: ReleaseStage
  commitSha: string
  statusFingerprint: string
  errorCode: string
  errorMessage: string
  /** 外部自动化页面；本地任务成功仅表示已经完成交接。 */
  automationUrl?: string
  completionMessage?: string
  createdAt: string
  finishedAt: string | null
}

export interface ReleaseVersion {
  versionGroupId: string
  versionGroupName: string
  targetVersion: string
  tagName: string
}

export interface ReleaseLog {
  id: number
  releaseRunId: string
  ts: string
  stream: 'event' | 'stdout' | 'stderr' | 'error'
  text: string
}

export interface ReleaseTargetRun {
  releaseRunId: string
  targetId: string
  build: boolean
  package: boolean
  publish: boolean
  deploy: boolean
  checkDone: boolean
  buildDone: boolean
  packageDone: boolean
  publishDone: boolean
  deployDone: boolean
  status: 'waiting' | 'queued' | 'running' | 'triggered' | 'remote_pending' | 'handed_off' | 'succeeded' | 'failed'
  stage: string
  errorCode: string
  errorMessage: string
  startedAt: string | null
  finishedAt: string | null
}

export interface ReleaseArtifact {
  id: number
  releaseRunId: string
  targetId: string
  path: string
  sizeBytes: number
  sha256: string
  createdAt: string
}

export interface ReleaseAutomationStatus {
  provider: string
  workflow: string
  url: string
  state: string
  message: string
}

export interface ReleaseRunView {
  run: ReleaseRun
  targets: ReleaseTargetRun[]
  artifacts: ReleaseArtifact[]
  logs: ReleaseLog[]
  automation?: ReleaseAutomationStatus
}

export interface CreateReleaseBody {
  targetVersion: string
  versions: Array<{ versionGroupId: string; targetVersion: string }>
  createTag: boolean
  pushRemote: boolean
  versionMode: ReleaseVersionMode
  selectedTargets: SelectedReleaseTarget[]
  selectedPaths: string[]
  commitMessage: string
  releaseNotes: string
  releaseNotesConfirmed: boolean
  statusFingerprint: string
  externalActionsConfirmed: boolean
}

/** 配置导出快照 */
export interface ExportSnapshot {
  exportedAt: string
  apps: AppView[]
  groups: Group[]
  settings: Record<string, string>
  releaseProfiles?: ReleaseProfile[]
}
