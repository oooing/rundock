import type { ReleaseLog } from '@/types'

const diskFull = /os error 112|no space (?:left )?on (?:the )?device|磁盘空间不足|there is not enough space on the disk|error_disk_full|code enospc|enospc:/i
const buildStages = new Set(['building_targets', 'target_build', 'target_package', 'local_build', 'local_package'])

// Old sidecars saved only a generic command error. Read the final command's
// evidence, without rewriting history or carrying a previous attempt's error
// into a later failure. Only fixed copy and a drive letter reach the summary.
export function releaseFailureAdvice(run: { status: string; stage: string; errorCode: string },
  logs: ReadonlyArray<Pick<ReleaseLog, 'stream' | 'text'>> = []) {
  if (run.status !== 'failed' || !buildStages.has(run.stage)
    || !['target_step_failed', 'local_build_failed', 'build_disk_full'].includes(run.errorCode)) return null

  let attemptStart = 0
  for (let i = logs.length - 1; i >= 0; i--) {
    if (logs[i].stream === 'event' && /重试[：:]|正在构建|正在打包|正在准备发布/.test(logs[i].text)) {
      attemptStart = i + 1
      break
    }
  }
  let output = ''
  for (let i = logs.length - 1; i >= attemptStart; i--) {
    if (['stdout', 'stderr'].includes(logs[i].stream) && logs[i].text.trim()) {
      output = logs[i].text
      break
    }
  }
  const evidence = output.split(/\r?\n/).filter(line => diskFull.test(line))
  if (run.errorCode !== 'build_disk_full' && !evidence.length) return null
  const drive = evidence.map(line => /(?:^|[\s`"'(=])([a-z]):[\\/]/i.exec(line)?.[1]).find(Boolean)?.toUpperCase()
  return {
    reason: drive ? '生成文件时，{0} 盘空间不足。' : '生成文件时，构建磁盘空间不足。',
    values: drive ? [drive] : [],
    action: '清理该磁盘或更换构建临时目录后，再重新构建。',
  }
}
