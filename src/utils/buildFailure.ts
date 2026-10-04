import type { ReleaseLog, ReleaseRun } from '@/types'
import type { ReleaseTargetRun } from '@/types/releaseExecution';
import type { CloudBuildStatus } from '@/types';

export function cloudFailureText(build: CloudBuildStatus): string {
  return [
    'RunDock — cloud build failure',
    `Project: ${build.appName}`, `Version: ${build.version}`, `Status: ${build.state}`,
    build.summary, build.url ? `Build URL: ${build.url}` : '',
    build.releaseRunId ? `Release ID: ${build.releaseRunId}` : '',
    'Cloud log contents are not included; open the build URL for full logs.',
  ].filter(Boolean).join('\n')
}

export function localFailureText(name: string, run: ReleaseRun, targets: ReleaseTargetRun[], logs: ReleaseLog[]): string {
  const tail = logs.slice(-200).map(line => `[${line.ts}] [${line.stream}] ${line.text}`).join('\n')
  const excerpt = tail.slice(-24000)
  return [
    'RunDock — release/build failure', `Project: ${name}`,
    `Version: ${run.versions?.map(v => v.tagName).join(', ') || run.tagName || run.targetVersion || '—'}`,
    `Release ID: ${run.id}`, `Stage: ${run.stage}`, `Error code: ${run.errorCode || '—'}`,
    run.commitSha ? `Commit: ${run.commitSha}` : '', run.errorMessage,
    ...targets.filter(target => target.status === 'failed').map(target =>
      `Target: ${target.targetId}\nStage: ${target.stage}\nError code: ${target.errorCode || '—'}\n${target.errorMessage}`),
    excerpt ? `\nLogs${logs.length > 200 || tail.length > 24000 ? ' (tail; truncated)' : ''}:\n${excerpt}` : '',
  ].filter(Boolean).join('\n')
}
