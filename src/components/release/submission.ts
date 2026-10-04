import { api, ApiError } from '@/api/http';
import { tr } from '@/i18n';
import { rememberReleaseSession } from '@/utils/releaseSession';
import { nextTick } from 'vue';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installSubmission(ctx: ReleaseContext) {
    ctx.submitRelease = async function () {
        if (!ctx.canSubmit.value || ctx.autoSubmitting.value || ctx.checkingCandidate.value || ctx.disposed)
            return;
        ctx.autoSubmitting.value = true;
        const confirmedPlan = ctx.submissionPlanSignature.value;
        let stopped = true;
        try {
            const checked = ctx.candidateReady.value || await ctx.inspectCandidate();
            if (!checked || ctx.disposed || !ctx.candidateReady.value)
                return;
            if (confirmedPlan !== ctx.submissionPlanSignature.value || !ctx.canPublish.value) {
                ctx.error.value = tr('发布内容已变化，请核对后再次确认。');
                return;
            }
            await ctx.publish();
            stopped = false;
        }
        finally {
            ctx.autoSubmitting.value = false;
            if (stopped && !ctx.disposed) {
                await nextTick();
                ctx.bodyRef.value?.querySelector('.candidate-result')?.scrollIntoView({ block: 'center' });
            }
        }
    };
    ctx.publish = async function () {
        const pf = ctx.preflight.value;
        if (!pf || !ctx.canPublish.value)
            return;
        ctx.publishing.value = true;
        ctx.error.value = '';
        ctx.errorCode.value = '';
        ctx.versionPlanNotice.value = '';
        try {
            await api.saveReleaseProfile(ctx.props.app.id, ctx.profileBody());
            rememberReleaseSession({ appId: ctx.props.app.id, submittedAt: Date.now() });
            const run = await api.createRelease(ctx.props.app.id, {
                ...ctx.candidateRequest.value, candidateId: ctx.candidate.value?.id,
                targetVersion: ctx.createTag.value ? ctx.primaryTargetVersion.value : '',
                versions: ctx.createTag.value ? ctx.plannedVersions.value.map((version) => ({ versionGroupId: version.versionGroupId, targetVersion: version.targetVersion })) : [],
                createTag: ctx.createTag.value, versionMode: ctx.versionMode.value,
                buildMode: ctx.gitOnly.value ? 'none' : ctx.buildMode.value, pushRemote: ctx.pushRemote.value,
                selectedTargets: ctx.selectedTargets.value, selectedPaths: ctx.selectedPaths.value, commitMessage: ctx.commitMessage.value, statusFingerprint: pf.statusFingerprint,
                releaseNotes: ctx.createTag.value ? ctx.releaseNotes.value.trim() : '',
                releaseNotesConfirmed: ctx.createTag.value,
                externalActionsConfirmed: ctx.hasExternalAction.value,
            });
            if (!ctx.disposed)
                ctx.showRun(run);
        }
        catch (reason) {
            rememberReleaseSession({ appId: ctx.props.app.id });
            if (ctx.disposed)
                return;
            if (reason instanceof ApiError && reason.code === 'version_plan_changed' && reason.preflight) {
                ctx.applyPreflight(reason.preflight, false, false);
                ctx.versionPlanNotice.value = tr('版本建议已更新。请核对版本与更新说明，再次确认后继续。');
            }
            else {
                ctx.error.value = ctx.releaseErrorMessage(reason);
                ctx.errorCode.value = reason instanceof ApiError ? reason.code : '';
                if (ctx.errorCode.value === 'status_changed')
                    ctx.preflightStale.value = true;
                if (['status_changed', 'candidate_stale', 'candidate_not_found', 'candidate_not_accepted'].includes(ctx.errorCode.value) && ctx.candidate.value) {
                    ctx.candidate.value = { ...ctx.candidate.value, status: 'stale', accepted: false, canSaveProgress: false };
                    ctx.candidateSignature.value = '';
                    ctx.reviewSignature.value = '';
                    ctx.findingDecisions.value = {};
                }
            }
            await nextTick();
            ctx.bodyRef.value?.scrollTo({ top: 0 });
        }
        finally {
            ctx.publishing.value = false;
        }
    };
    ctx.prepareLocalCommit = function () {
        ctx.changeReleaseIntent('save-progress');
        // Change the visible plan only. The normal submit button remains the final action.
        ctx.gitOnly.value = true;
        ctx.createTag.value = false;
        ctx.pushRemote.value = false;
        if (ctx.errorCode.value.startsWith('remote_') || ctx.errorCode.value === 'fetch_failed') {
            ctx.error.value = '';
            ctx.errorCode.value = '';
        }
        if (!ctx.commitMessageDirty.value)
            ctx.setDefaultCommitMessage();
    };
    ctx.releaseErrorMessage = function (reason: unknown) {
        const message = ctx.messageOf(reason);
        if (!(reason instanceof ApiError))
            return message;
        const titles: Record<string, string> = {
            remote_timeout: tr('远程检查超时。请检查网络或代理，也可关闭“提交后上传”在本机完成。'),
            remote_auth_failed: tr('远程仓库认证失败。请检查 Git 凭据和仓库访问权限。'),
            remote_branch_missing: tr('远程仓库没有当前分支。请确认分支名称或先建立远程分支。'),
            remote_network_failed: tr('无法连接远程仓库。请检查网络、代理或证书设置。'),
            remote_check_cancelled: tr('远程检查已取消。'),
            remote_check_failed: tr('远程检查失败。请查看下方 Git 返回的原因。'),
        };
        const title = titles[reason.code];
        if (!title)
            return message;
        const detail = message.split('\n').slice(1).join('\n').trim();
        return detail ? `${title}\n${detail}` : title;
    };
}
