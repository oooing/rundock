import type { ReleaseCandidate, ReleaseFileClassification } from '@/types'

type ReleaseCheck = ReleaseCandidate['checkResults'][number]

export function dependencyPresentation(finding: ReleaseCandidate['dependencyFindings'][number], files: ReleaseFileClassification[]) {
  const source = files.find(file => file.path === finding.path)
  const target = files.find(file => file.path === finding.missing)
  const canExclude = !!source && !source.tracked && (source.category === 'local' ||
    source.reasons.some(reason => reason.includes('本地验证资料')))
  if (finding.kind === 'unreadable') return {
    title: '无法读取', canExclude: false,
    suggestion: '选中的文件无法读取，请检查文件权限或刷新列表后重试。',
  }
  if (canExclude) return {
    title: '资料不完整', canExclude,
    suggestion: '本机资料被选入检查，配套文件不齐；不用于发布时，可跳过此文件。',
  }
  if (finding.kind === 'not_selected' || (!finding.kind && target)) return {
    title: '文件未选', canExclude: false,
    suggestion: target?.category === 'recommend'
      ? '配套文件在本机，但未选入；请加入本次保存，再重新检查。'
      : '配套文件未选入；请到文件列表确认用途，需要发布时再选择保存。',
  }
  return {
    title: finding.kind === 'unresolved' ? '引用未找到' : '缺少文件', canExclude: false,
    suggestion: '请补齐配套文件或修正引用；若由构建生成，请先生成所需文件。',
  }
}

export function isFileReviewCheck(check: ReleaseCheck) {
  // Accept old backend snapshots too, but never relabel unrelated failures.
  return check.id === 'rundock:scope' && ['blocked', 'failed'].includes(check.status) &&
    (check.reason || '').includes('仍有文件用途需要确认')
}

export function hasOnlyFileReviewBlock(candidate: ReleaseCandidate | null) {
  return !!candidate && ['blocked', 'failed'].includes(candidate.status) && candidate.checkResults.some(isFileReviewCheck) &&
    !candidate.sensitiveFindings.length && !candidate.dependencyFindings.some(item => item.blocked) &&
    !candidate.checkResults.some(check => check.required && ['failed', 'unverified', 'blocked'].includes(check.status) && !isFileReviewCheck(check))
}

export function releaseCheckPresentation(check: ReleaseCheck) {
  if (isFileReviewCheck(check)) return {
    label: '待确认文件',
    reason: '还有文件尚未确定是否提交，这不是编译或测试失败。',
    suggestion: '请选择“提交”“本次不提交”或“以后忽略”；处理完成后再检查。',
  }
  const missingTool = /必需检查工具不可用|找不到检查工具/.test(check.reason || '')
  const cannotStart = /无法启动检查命令|检查工作目录无效或不可用|无法建立检查进程/.test(check.reason || '')
  const labels: Record<string, string> = {
    passed: '已通过', failed: '检查失败', unverified: '尚未验证', blocked: '检查被阻止',
    skipped: '已跳过', running: '检查中…', pending: '等待检查', cancelled: '已取消', stale: '检查结果已过期',
  }
  return {
    label: missingTool || cannotStart ? '无法开始检查' : labels[check.status] || check.status,
    reason: check.reason?.trim() || (check.log?.trim() ? '未返回具体原因，请查看执行日志。' : '未返回具体原因，也没有执行日志；请重新检查。'),
    suggestion: missingTool
      ? /pwsh/i.test(check.reason || '')
        ? '安装 PowerShell 7 并加入 PATH；如已安装，请确认启动 RunDock 的环境可运行 pwsh，再重新打开 RunDock 并检查。'
        : '确认检查命令所需工具已安装并加入 PATH；如刚安装或修改 PATH，请重新打开 RunDock 后再检查。'
      : cannotStart
        ? '检查尚未启动，请根据上述原因处理环境或配置后重试。'
      : check.status === 'unverified' || check.status === 'blocked'
        ? '先处理上述环境或配置问题，再重新检查；当前结果不代表代码测试失败。'
        : '根据具体原因和执行日志修复后，点击“重新检查”。',
  }
}

export function groupSensitiveFindings(findings: ReleaseCandidate['sensitiveFindings']) {
  const groups = new Map<string, ReleaseCandidate['sensitiveFindings']>()
  for (const finding of findings) groups.set(finding.path, [...(groups.get(finding.path) || []), finding])
  return [...groups].map(([path, findings]) => ({ path, findings }))
}

export function hasReleaseIssues(candidate: ReleaseCandidate | null) {
  return !!candidate && (candidate.sensitiveFindings.length > 0 ||
    candidate.dependencyFindings.some(finding => finding.blocked) ||
    candidate.checkResults.some(check => check.required && ['failed', 'unverified', 'blocked'].includes(check.status)) ||
    ['failed', 'blocked'].includes(candidate.status))
}
