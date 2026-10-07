import { tr } from '@/i18n'
import type { ReleaseConfig, ReleaseTarget } from '@/types'
import { releaseTargetLabel } from './releasePresentation'

export type ReleaseSetupState = 'configured' | 'missing' | 'incomplete' | 'disabled'
export interface ReleaseSetupMode { state: ReleaseSetupState; reason: string }
export interface ReleaseSetupSummary {
  id: string
  name: string
  local: ReleaseSetupMode
  cloud: ReleaseSetupMode
}

function targetOS(target: ReleaseTarget): string {
  const systems = [...new Set((target.runner.os || []).map(value => {
    const os = value.trim().toLowerCase()
    return ['mac', 'macos'].includes(os) ? 'darwin' : os
  }).filter(Boolean))].sort()
  return !systems.length || systems.includes('any') ? '' : systems.join(',')
}

function modeSummary(targets: ReleaseTarget[], mode: 'local' | 'cloud', config: ReleaseConfig): ReleaseSetupMode {
  if (!targets.length) return { state: 'missing', reason: mode === 'local' ? tr('未配置本地构建') : tr('未配置云端构建') }
  const enabled = targets.filter(target => target.enabled !== false)
  if (!enabled.length) return { state: 'disabled', reason: tr('已停用') }
  const issues = enabled.map(target => {
    if (mode === 'cloud') {
      const command = (target.steps.publish || '').trim()
      if (/^(tag-push|branch-push)$/i.test(command)) return ''
      if (!/^workflow-dispatch:[^/\\\s?#]+\.ya?ml$/.test(command) || config.automation?.trigger !== 'dispatch') return tr('缺少云端构建流程')
      return config.automation.account?.trim() ? '' : tr('缺少云端构建账号')
    }
    if (target.runner.type.trim().toLowerCase() !== 'local') return tr('暂不支持此构建方式')
    if (!(target.steps.build || '').trim() && !(target.steps.package || '').trim()) return tr('缺少构建命令')
    if (!(target.artifacts || []).some(path => path.trim()) && !(target.artifactRules || []).some(rule => rule.pattern.trim())) return tr('缺少产物位置')
    return ''
  })
  // Alternative definitions may coexist. One complete definition is enough to
  // describe that mode as configured; this never claims a build was verified.
  return issues.includes('') ? { state: 'configured', reason: tr('已配置') }
    : { state: 'incomplete', reason: [...new Set(issues)].join('；') }
}

/** Summarize configuration only. Does not save, execute, or verify a build. */
export function getReleaseSetupSummary(config: ReleaseConfig | null): ReleaseSetupSummary[] {
  if (!config) return []
  const bases = new Map<string, ReleaseTarget[]>()
  for (const target of config.targets) {
    const directory = (target.workingDir || '.').replace(/\\/g, '/').replace(/^(\.\/)+/, '').replace(/\/$/, '') || '.'
    const key = JSON.stringify([releaseTargetLabel(target.name) || target.id, target.kind.trim().toLowerCase(), target.versionGroup, directory])
    bases.set(key, [...(bases.get(key) || []), target])
  }
  const rows: Array<ReleaseSetupSummary & { version: string; directory: string; os: string }> = []
  for (const [base, targets] of bases) {
    const [name, , version, directory] = JSON.parse(base) as string[]
    const specificSystems = [...new Set(targets.map(targetOS).filter(Boolean))]
    const groups = new Map<string, ReleaseTarget[]>()
    for (const target of targets) {
      // Cloud runners often omit OS. Pair that definition only when this product
      // has one unambiguous OS variant; never merge Windows and macOS settings.
      const os = targetOS(target) || (specificSystems.length === 1 ? specificSystems[0] : '')
      groups.set(os, [...(groups.get(os) || []), target])
    }
    for (const [os, members] of groups) {
      const cloud = members.filter(target => target.runner.type.trim().toLowerCase() === 'git-push')
      const local = members.filter(target => target.runner.type.trim().toLowerCase() !== 'git-push')
      rows.push({ id: JSON.stringify([base, os]), name, local: modeSummary(local, 'local', config), cloud: modeSummary(cloud, 'cloud', config), version, directory, os })
    }
  }
  return rows.map(row => {
    const peers = rows.filter(other => other.name === row.name)
    const qualifiers: string[] = []
    if (new Set(peers.map(other => other.version)).size > 1) qualifiers.push(config.versionGroups.find(group => group.id === row.version)?.name || row.version)
    if (new Set(peers.map(other => other.directory)).size > 1) qualifiers.push(row.directory)
    if (new Set(peers.map(other => other.os)).size > 1) qualifiers.push(row.os ? row.os.split(',').map(os => ({ windows: 'Windows', darwin: 'macOS', linux: 'Linux' })[os] || os).join(' / ') : tr('不限系统'))
    return { id: row.id, name: qualifiers.length ? `${row.name} · ${qualifiers.join(' · ')}` : row.name, local: row.local, cloud: row.cloud }
  })
}
