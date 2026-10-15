import { api } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseContext } from './context';
import type { ReleaseCandidateRequest } from '@/types';

export interface PreparationDraft {
  request: ReleaseCandidateRequest;
  commitMessage: string; releaseNotes: string;
  syncPolicy: 'auto' | 'local';
}
export interface ReleasePreparation {
  id: string; appId: string; revision: number; status: string;
  candidateId: string; runId: string; message: string;
  draft: PreparationDraft; updatedAt: string;
}
export function installPreparation(ctx: ReleaseContext) {
  let writes = Promise.resolve();
  const revisions = new Map<string, number>();
  ctx.savePreparation = (status: string, runId = '') => {
    // Capture now; serial writes prevent a late response from restoring an old phase.
    const draft = JSON.parse(JSON.stringify({request:ctx.candidateRequest.value,
      commitMessage:ctx.commitMessage.value,releaseNotes:ctx.releaseNotes.value,syncPolicy:ctx.syncPolicy.value}));
    const candidateId = ctx.candidate.value?.id || '';
    // Keep diagnostics in their existing log; never duplicate command output or
    // secret excerpts in a navigation snapshot.
    const message = '';
    const previous = ctx.preparation.value?.runId ? null : ctx.preparation.value;
    const id = previous?.id || crypto.randomUUID();
    if (!previous) ctx.preparation.value = {id,appId:ctx.props.app.id,revision:0,status,candidateId,runId:'',message,draft,updatedAt:''};
    revisions.set(id, Math.max(revisions.get(id) || 0, previous?.revision || 0));
    const task = writes.catch(() => {}).then(async () => {
      const saved = await api.saveReleasePreparation(ctx.props.app.id, {
        id, appId:ctx.props.app.id,
        revision:revisions.get(id) || 0, status, candidateId,
        runId, message, draft, updatedAt:'',
      });
      revisions.set(id,saved.revision);
      if (ctx.preparation.value?.id === id) ctx.preparation.value=saved;
      ctx.preparationHistory.value=[saved,...ctx.preparationHistory.value.filter(item=>item.id!==saved.id)].slice(0,20);
    });
    writes=task;return task;
  };
  ctx.restorePreparation = async (saved: ReleasePreparation) => {
    if(ctx.autoSubmitting.value || ctx.publishing.value || ctx.checkingCandidate.value) return;
    ctx.autoSubmitting.value=true;
    try {
    // Reconcile before restoring: a lost response may already have created a run.
    try {
      const latest = await api.listReleasePreparations(ctx.props.app.id);
      if (ctx.disposed) return;
      ctx.preparationHistory.value = latest;
      saved = latest.find(item => item.id === saved.id) || saved;
    } catch(reason) { ctx.error.value=tr('无法确认上次提交结果，请恢复连接后重试。'); return; }
    if(saved.status === 'submitting' && !saved.runId) {
      ctx.submissionUncertain.value=true;
      ctx.error.value=tr('提交结果待确认，请稍后核对；不会重复发布。'); return;
    }
    ctx.submissionUncertain.value=false;
    if(saved.runId) {
      try {ctx.showRun((await api.getReleaseRun(saved.runId)).run)}
      catch(reason){ctx.error.value=ctx.messageOf(reason)}
      return;
    }
    const draft=saved.draft, request=draft?.request;
    if(!request || !Array.isArray(request.selectedTargets) || !Array.isArray(request.versions)
      || !Array.isArray(request.selectedPaths) || !ctx.preflight.value) return;
    ctx.preparation.value=saved;
    // Restoring choices never trusts a persisted acceptance result or starts work.
    ctx.candidate.value=null;ctx.candidateSignature.value='';
    ctx.releaseIntent.value=request.intent || 'formal';
    ctx.gitOnly.value=ctx.releaseIntent.value==='save-progress';
    ctx.createTag.value=request.createTag;
    ctx.buildMode.value=request.buildMode==='local'?'local':'github';
    ctx.versionMode.value=request.versionMode;ctx.syncPolicy.value=draft.syncPolicy;
    ctx.checksEnabled.value=!request.skipChecks;
    ctx.applySyncPolicy();
    for(const id of Object.keys(ctx.targetChoices.value)) {
      const choice=request.selectedTargets.find(item=>item.targetId===id);
      ctx.targetChoices.value[id]=choice?{...choice,selected:true}:{selected:false,build:false,package:false,publish:false,deploy:false};
    }
    ctx.versionInputs.value=Object.fromEntries(request.versions.map(item=>[item.versionGroupId,item.targetVersion]));
    // Preserve the intended version; a changed repository must require explicit review.
    ctx.versionMode.value='manual';
    ctx.selected.value=Object.fromEntries(request.selectedPaths.map(path=>[path,true]));
    ctx.manualDecisions.value=request.manualDecisions || [];
    ctx.sensitiveExceptions.value=request.sensitiveExceptions || [];
    ctx.commitMessage.value=draft.commitMessage;ctx.commitMessageDirty.value=true;
    ctx.onReleaseNotesInput();ctx.releaseNotes.value=draft.releaseNotes;
    ctx.error.value='';
    ctx.versionPlanNotice.value=tr('已恢复上次选择，尚未发布。点击继续后会重新核对当前文件。');
    ctx.releaseTab.value='publish';
    } finally {ctx.autoSubmitting.value=false}
  };
}
