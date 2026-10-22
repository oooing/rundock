import { api, ApiError } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseRun } from '@/types';
import { rememberReleaseSession } from '@/utils/releaseSession';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installProgress(ctx: ReleaseContext) {
    let viewEpoch = 0;
    ctx.showRun = function (run: ReleaseRun) {
        viewEpoch += 1;
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
        ctx.runConnectionError.value = '';
        ctx.submissionUncertain.value = false;
        rememberReleaseSession({ appId: ctx.props.app.id, runId: run.id });
        ctx.schedulePoll(0);
    };
    ctx.historyStatus = function (run: ReleaseRun) {
        if (run.status === 'succeeded')
            return run.pushRemote ? tr("查看发布结果") : tr("本地完成");
        if (/cancelled/.test(run.errorCode || '')) return tr('已取消');
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
        const epoch = viewEpoch;
        if (!run || ctx.disposed)
            return;
        try {
            const lastId = ctx.logs.value.length ? ctx.logs.value[ctx.logs.value.length - 1].id : 0;
            const view = await api.getReleaseRun(run.id, lastId);
            if (ctx.disposed || epoch !== viewEpoch || ctx.activeRun.value?.id !== run.id)
                return;
            ctx.activeRun.value = view.run;
            ctx.runConnectionError.value = '';
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
                const history = await api.listReleases(ctx.props.app.id).catch(() => null);
                if (history && !ctx.disposed && epoch === viewEpoch) ctx.history.value = history;
                if (ctx.disposed || epoch !== viewEpoch || ctx.activeRun.value?.id !== run.id)
                    return;
                if (view.run.status === 'succeeded' && (ctx.automationHandedOff.value && !ctx.cloudBuildSettled.value
                    || ctx.runDeliveries.value.some(item => ['pending','unverified','deploying','deployment_requested'].includes(item.syncState))))
                    ctx.schedulePoll(15000);
            }
        }
        catch (reason) {
            if (ctx.disposed || epoch !== viewEpoch || ctx.activeRun.value?.id !== run.id)
                return;
            ctx.runConnectionError.value = ctx.messageOf(reason);
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
    // Leaving a historical record only clears its viewer, never the release draft.
    ctx.leaveRunView = function () {
        viewEpoch += 1;
        if (ctx.pollTimer)
            clearTimeout(ctx.pollTimer);
        ctx.pollTimer = null;
        if (!ctx.activeRun.value || !['queued', 'running'].includes(ctx.activeRun.value.status))
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
    };
    ctx.startNew = function () {
        ctx.preparation.value = null;
        const run = ctx.activeRun.value;
        if (run?.status === 'failed' && !run.commitSha
            && ['preparing', 'delivery_connecting', 'delivery_preflight'].includes(run.stage)
            && ctx.candidateReady.value) {
            // A connection failure did not consume the accepted candidate. Keep
            // the draft, exceptions and completed checks; publish validates the
            // repository fingerprint again before any changes.
            ctx.leaveRunView();
            ctx.error.value = '';
            return;
        }
        ctx.candidate.value = null;
        ctx.candidateSignature.value = '';
        ctx.leaveRunView();
        ctx.cancelReleaseNotesDraft();
        ctx.releaseNotes.value = '';
        ctx.releaseNotesDirty.value = false;
        ctx.releaseNotesStale.value = false;
        ctx.releaseNotesGeneratedFor.value = '';
        ctx.releaseNotesError.value = '';
        ctx.commitMessageDirty.value = false;
        void ctx.load();
    };
}
