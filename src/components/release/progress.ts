import { api, ApiError } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseRun } from '@/types';
import { rememberReleaseSession } from '@/utils/releaseSession';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installProgress(ctx: ReleaseContext) {
    ctx.showRun = function (run: ReleaseRun) {
        if (ctx.pollTimer)
            clearTimeout(ctx.pollTimer);
        ctx.activeRun.value = run;
        ctx.logs.value = [];
        ctx.runTargets.value = [];
        ctx.runArtifacts.value = [];
        ctx.runAutomation.value = null;
        ctx.runDeliveries.value = [];
        ctx.cloudBuild.value = null;
        ctx.retryMetadataLoaded.value = false;
        ctx.retryConfirmationRequired.value = undefined;
        ctx.retryConfirmationTargets.value = [];
        ctx.error.value = '';
        rememberReleaseSession({ appId: ctx.props.app.id, runId: run.id });
        ctx.schedulePoll(0);
    };
    ctx.historyStatus = function (run: ReleaseRun) {
        if (run.status === 'succeeded')
            return run.pushRemote ? tr("已推送") : tr("本地完成");
        return run.status === 'failed' ? tr("失败") : tr("进行中");
    };
    ctx.schedulePoll = function (delay = 700) {
        if (ctx.disposed)
            return;
        if (ctx.pollTimer)
            clearTimeout(ctx.pollTimer);
        ctx.pollTimer = setTimeout(() => void ctx.poll(), delay);
    };
    ctx.poll = async function () {
        const run = ctx.activeRun.value;
        if (!run || ctx.disposed)
            return;
        try {
            const lastId = ctx.logs.value.length ? ctx.logs.value[ctx.logs.value.length - 1].id : 0;
            const view = await api.getReleaseRun(run.id, lastId);
            if (ctx.disposed || ctx.activeRun.value?.id !== run.id)
                return;
            ctx.activeRun.value = view.run;
            ctx.runTargets.value = view.targets || [];
            ctx.runArtifacts.value = view.artifacts || [];
            ctx.runAutomation.value = view.automation || null;
            ctx.runDeliveries.value = view.deliveries || [];
            ctx.cloudBuild.value = view.cloudBuild || null;
            ctx.retryConfirmationRequired.value = view.retryConfirmationRequired;
            ctx.retryConfirmationTargets.value = view.retryConfirmationTargets || [];
            ctx.retryMetadataLoaded.value = true;
            ctx.logs.value = [...ctx.logs.value, ...(view.logs || [])];
            if (view.run.status === 'queued' || view.run.status === 'running')
                ctx.schedulePoll();
            else {
                ctx.history.value = await api.listReleases(ctx.props.app.id);
                if (view.run.status === 'succeeded' && ctx.automationHandedOff.value && !ctx.cloudBuildSettled.value)
                    ctx.schedulePoll(15000);
            }
        }
        catch (reason) {
            if (ctx.disposed)
                return;
            ctx.error.value = ctx.messageOf(reason);
            ctx.schedulePoll(1500);
        }
    };
    ctx.retry = async function (externalActionsConfirmed = false) {
        if (!ctx.activeRun.value || !ctx.retryable.value || ctx.retrying.value || !ctx.retryMetadataLoaded.value)
            return;
        if (ctx.customRetryConfirmation.value && !externalActionsConfirmed) {
            ctx.confirmAction.value = 'retry';
            return;
        }
        const runId = ctx.activeRun.value.id;
        ctx.retrying.value = true;
        ctx.error.value = '';
        try {
            // Clicking retry authorizes resuming this same upload. Older sidecars also
            // require the flag for Git uploads; custom commands retain explicit consent.
            const run = await api.retryRelease(runId, !ctx.customRetryConfirmation.value || externalActionsConfirmed);
            if (!ctx.disposed && ctx.activeRun.value?.id === runId) {
                ctx.activeRun.value = run;
                ctx.schedulePoll(0);
            }
        }
        catch (reason) {
            if (!ctx.disposed && ctx.activeRun.value?.id === runId) {
                ctx.error.value = ctx.releaseErrorMessage(reason);
                if (reason instanceof ApiError && reason.code === 'external_actions_confirmation_required') {
                    ctx.retryConfirmationRequired.value = true;
                    ctx.schedulePoll(0);
                }
            }
        }
        finally {
            ctx.retrying.value = false;
        }
    };
    ctx.confirmSensitiveAction = async function () {
        const action = ctx.confirmAction.value;
        ctx.confirmAction.value = null;
        if (action === 'retry')
            await ctx.retry(true);
        else if (action === 'regenerate-notes')
            await ctx.generateReleaseNotesDraft(true, true);
    };
    ctx.startNew = function () {
        ctx.candidate.value = null;
        ctx.candidateSignature.value = '';
        if (ctx.pollTimer)
            clearTimeout(ctx.pollTimer);
        rememberReleaseSession({ appId: ctx.props.app.id });
        ctx.confirmAction.value = null;
        ctx.activeRun.value = null;
        ctx.logs.value = [];
        ctx.runTargets.value = [];
        ctx.runArtifacts.value = [];
        ctx.runAutomation.value = null;
        ctx.runDeliveries.value = [];
        ctx.cloudBuild.value = null;
        ctx.retryMetadataLoaded.value = false;
        ctx.retryConfirmationRequired.value = undefined;
        ctx.retryConfirmationTargets.value = [];
        ctx.releaseNotes.value = '';
        ctx.releaseNotesDirty.value = false;
        ctx.releaseNotesStale.value = false;
        ctx.releaseNotesGeneratedFor.value = '';
        ctx.releaseNotesError.value = '';
        ctx.commitMessageDirty.value = false;
        void ctx.load(false);
    };
}
