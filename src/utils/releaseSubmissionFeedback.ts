// These messages describe the pre-submit form, never a running release.
export function releaseSubmissionFeedback(state: {
  busy: boolean; error: string; notice: string; needsAttention: boolean;
  stale: boolean; ready: boolean; status?: string; formal: boolean;
}) {
  if (state.busy) return null;
  if (state.error) return { tone: 'error', text: state.error };
  if (state.notice) return { tone: 'notice', text: state.notice };
  if (state.status === 'cancelled') return { tone: 'notice', text: '检查已取消，尚未提交。' };
  if (state.needsAttention) return { tone: 'notice', text: '尚未提交，请先处理检查发现的问题。' };
  if (state.stale) return { tone: 'notice', text: '检查结果已失效，尚未提交。请重新检查。' };
  if (state.ready) return { tone: 'ready', text: state.formal
    ? '内容已就绪，尚未发布。请点击“确认发布”。'
    : '内容已就绪，尚未提交。请点击提交按钮。' };
  return null;
}
