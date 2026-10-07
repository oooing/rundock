import type { ReleaseCandidate } from '@/types'

type ReleaseCheck = ReleaseCandidate['checkResults'][number]

export function releaseCheckPresentation(check: ReleaseCheck) {
  const missingTool = /必需检查工具不可用|找不到检查工具/.test(check.reason || '')
  const labels: Record<string, string> = {
    passed: '已通过', failed: '检查失败', unverified: '尚未验证', blocked: '检查被阻止',
    skipped: '已跳过', running: '检查中…', pending: '等待检查', cancelled: '已取消', stale: '检查结果已过期',
  }
  return {
    label: missingTool ? '无法开始检查' : labels[check.status] || check.status,
    reason: check.reason?.trim() || (check.log?.trim() ? '未返回具体原因，请查看执行日志。' : '未返回具体原因，也没有执行日志；请重新检查。'),
    suggestion: missingTool
      ? /pwsh/i.test(check.reason || '')
        ? '安装 PowerShell 7 并加入 PATH；如已安装，请确认启动 RunDock 的环境可运行 pwsh，再重新打开 RunDock 并检查。'
        : '确认检查命令所需工具已安装并加入 PATH；如刚安装或修改 PATH，请重新打开 RunDock 后再检查。'
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
