import { api, ApiError } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseTarget, ReleaseVersionMode } from '@/types';
import { readReleasePreferences, writeReleasePreferences, releasePreferenceKey } from '@/utils/releasePreferences';
import type { ReleasePreferences } from '@/utils/releasePreferences';
import type { ExecutionPhase, ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installPreferences(ctx: ReleaseContext) {
    let loadRevision = 0;
    ctx.preferenceKey = function () {
        return releasePreferenceKey(ctx.props.app.id);
    };
    ctx.readLocalPreferences = () => readReleasePreferences(ctx.props.app.id);
    const targetSelections = { ...ctx.readLocalPreferences().releaseTargets };
    ctx.captureTargetPreferences = function () {
        if (!ctx.profileReady.value) return;
        targetSelections[ctx.buildMode.value] = ctx.configuredTargets.value
            .filter(target => target.runner.type.trim().toLowerCase() === (ctx.buildMode.value === 'github' ? 'git-push' : 'local')
                && ctx.targetChoices.value[target.id]?.selected).map(target => target.id);
    };
    ctx.restoreTargetPreferences = function () {
        const ids = targetSelections[ctx.buildMode.value];
        if (!ids) return false;
        for (const target of ctx.configuredTargets.value) {
            const choice = ctx.targetChoices.value[target.id];
            if (choice) choice.selected = ids.includes(target.id) && ctx.targetAvailable(target);
        }
        return true;
    };
    ctx.rememberPreferences = function () {
        if (!ctx.profileReady.value)
            return;
        ctx.preferenceRevision += 1;
        ctx.preferenceStatus.value = 'saving';
        ctx.preferenceError.value = '';
        if (ctx.preferenceTimer)
            clearTimeout(ctx.preferenceTimer);
        ctx.preferenceTimer = setTimeout(ctx.saveRememberedPreferences, 250);
    };
    ctx.saveRememberedPreferences = function () {
        if (ctx.preferenceTimer)
            clearTimeout(ctx.preferenceTimer);
        ctx.preferenceTimer = null;
        const body = ctx.profileBody();
        const appId = ctx.props.app.id;
        ctx.captureTargetPreferences();
        const preferences: ReleasePreferences = { buildMode: ctx.buildMode.value, syncPolicy: ctx.syncPolicy.value,
            localSyncPolicy: ctx.localSyncPolicy.value,
            releaseIntent: ctx.releaseIntent.value, localVersionMode: ctx.localVersionMode.value,
            versionMode: ctx.versionMode.value, checksEnabled: ctx.checksEnabled.value,
            releaseTargets: structuredClone(targetSelections) };
        const revision = ctx.preferenceRevision;
        ctx.preferenceStatus.value = 'saving';
        ctx.preferenceError.value = '';
        ctx.preferenceSave = ctx.preferenceSave.catch(() => undefined).then(async () => {
            await api.saveReleaseProfile(appId, body);
            writeReleasePreferences(appId, preferences);
            if (!ctx.disposed && revision === ctx.preferenceRevision)
                ctx.preferenceStatus.value = 'saved';
        }).catch(reason => {
            if (!ctx.disposed && revision === ctx.preferenceRevision) {
                ctx.preferenceStatus.value = 'error';
                ctx.preferenceError.value = ctx.messageOf(reason);
            }
        });
        return ctx.preferenceSave;
    };
    ctx.closeModal = async function () {
        const locked = () => ctx.publishing.value || ctx.autoSubmitting.value || ctx.checkingCandidate.value || ctx.retrying.value
            || !!ctx.activeRun.value && ['queued', 'running'].includes(ctx.activeRun.value.status);
        if (locked())
            return;
        if (ctx.preferenceTimer)
            ctx.saveRememberedPreferences();
        await ctx.preferenceSave;
        // Closing dismisses the panel, not a save confirmation. A failed
        // autosave must not trap the user; it remains an error until dismissal.
        if (ctx.disposed || locked())
            return;
        ctx.emit('close');
    };
    ctx.setDefaultCommitMessage = function (force = false) {
        if (ctx.commitMessageDirty.value && !force)
            return;
        ctx.commitMessage.value = ctx.createTag.value ? `chore(release): ${ctx.plannedTagNames.value.join(', ')}` : `chore: update ${ctx.props.app.name}`;
        ctx.commitMessageDirty.value = false;
    };
    ctx.onCommitMessageInput = function () {
        ctx.commitMessageDirty.value = true;
    };
    ctx.syncVersionInputs = function () {
        const next = { ...ctx.versionInputs.value };
        for (const version of ctx.plannedVersions.value) {
            if (!next[version.versionGroupId] || ctx.versionMode.value === 'auto')
                next[version.versionGroupId] = version.suggestedVersion;
        }
        ctx.versionInputs.value = next;
    };
    ctx.unstageFiles = async function () {
        if (!ctx.preflight.value || ctx.unstaging.value || ctx.publishing.value)
            return;
        ctx.unstaging.value = true;
        ctx.unstageNotice.value = '';
        ctx.error.value = '';
        try {
            const pf = await api.unstageReleaseFiles(ctx.props.app.id, ctx.preflight.value.statusFingerprint);
            if (ctx.disposed)
                return;
            ctx.applyPreflight(pf);
            ctx.unstageNotice.value = tr('已取消暂存，文件修改已保留。请在下方重新选择本次提交的文件。');
        }
        catch (reason) {
            if (ctx.disposed)
                return;
            if (reason instanceof ApiError && reason.preflight)
                ctx.applyPreflight(reason.preflight);
            ctx.error.value = ctx.messageOf(reason);
        }
        finally {
            ctx.unstaging.value = false;
        }
    };
    ctx.load = async function () {
        const revision = ++loadRevision;
        const appId = ctx.props.app.id;
        const isCurrent = () => !ctx.disposed && revision === loadRevision && ctx.props.app.id === appId;
        ctx.loading.value = true;
        ctx.profileReady.value = false;
        ctx.error.value = '';
        ctx.errorCode.value = '';
        ctx.versionPlanNotice.value = '';
        ctx.configNotice.value = '';
        try {
            // Handle rejection immediately while history/config load independently;
            // report the original preflight failure through the dialog below.
            const localPreflight = Promise.allSettled([api.releasePreflight(appId, false)] as const);
            const [historyResult, configResult, preparationsResult] = await Promise.allSettled([
                api.listReleases(appId), api.getReleaseConfig(appId), api.listReleasePreparations(appId),
            ] as const);
            if (!isCurrent())
                return;
            // An unavailable journal/history is unknown, never an empty list.
            // Keep the form locked and expose Reload so an already submitted
            // task cannot silently turn into a fresh publish button.
            if (historyResult.status === 'rejected' || preparationsResult.status === 'rejected') {
                ctx.submissionUncertain.value = true;
                throw new Error(tr('无法确认上次提交结果，请恢复连接后重试。'));
            }
            ctx.history.value = historyResult.status === 'fulfilled' ? historyResult.value : [];
            ctx.preparationHistory.value = preparationsResult.status === 'fulfilled' ? preparationsResult.value : [];
            ctx.submissionUncertain.value = false;
            const pendingSubmission = ctx.preparationHistory.value.find(item => item.status === 'submitting' && !item.runId);
            if (pendingSubmission) {
                ctx.preparation.value = pendingSubmission;
                ctx.submissionUncertain.value = true;
                ctx.error.value = tr('提交结果待确认，请核对任务记录；不要重复发布。');
            }
            if (configResult.status === 'fulfilled') {
                ctx.configEndpointAvailable.value = true;
                ctx.applyReleaseConfig(configResult.value);
            }
            else {
                ctx.configEndpointAvailable.value = false;
                ctx.gitOnly.value = true;
                ctx.configNotice.value = tr("当前后端暂未启用自动发布配置，仍可继续使用基础 Git 提交与 Tag 功能。");
            }
            // Keep the editable form closed until preflight restores the saved
            // choices, versions and files. Partial data must never look ready.
            // "Publish" opens a new configuration, not the previous result.
            // Only active work resumes automatically; terminal runs remain in history.
            const resumable = ctx.history.value.find((run) => run.status === 'queued' || run.status === 'running');
            if (resumable)
                ctx.showRun(resumable);
            else {
                const submitted = ctx.preparationHistory.value.find(item => item.id === ctx.preparation.value?.id && item.runId);
                if (submitted) ctx.showRun((await api.getReleaseRun(submitted.runId)).run);
            }
            const [preflightResult] = await localPreflight;
            if (!isCurrent())
                return;
            if (preflightResult.status === 'rejected')
                throw preflightResult.reason;
            ctx.applyPreflight(preflightResult.value, true);
            ctx.profileReady.value = true;
            if (ctx.editedReleaseOptions.size)
                ctx.rememberPreferences();
        }
        catch (reason) {
            if (isCurrent())
                ctx.error.value = ctx.messageOf(reason);
        }
        finally {
            if (isCurrent())
                ctx.loading.value = false;
        }
    };
    ctx.profileBody = function () {
        return { buildMode: ctx.buildMode.value, syncPolicy: ctx.syncPolicy.value, remoteName: ctx.remoteName.value, versionStrategy: ctx.versionStrategy.value, preReleaseCommand: ctx.preReleaseCommand.value, createTag: ctx.createTag.value, versionMode: ctx.versionMode.value };
    };
    ctx.saveAndRecheck = async function () {
        ctx.savingProfile.value = true;
        ctx.error.value = '';
        ctx.preflightStale.value = true;
        try {
            await api.saveReleaseProfile(ctx.props.app.id, ctx.profileBody());
            ctx.applyPreflight(await api.releasePreflight(ctx.props.app.id, false));
        }
        catch (reason) {
            ctx.error.value = ctx.messageOf(reason);
        }
        finally {
            ctx.savingProfile.value = false;
        }
    };
    ctx.onVersionInput = function (groupID: string, value: string) {
        ctx.editedReleaseOptions.add('version');
        ctx.versionInputs.value = { ...ctx.versionInputs.value, [groupID]: value.trim() };
        ctx.setDefaultCommitMessage();
    };
    ctx.onCreateTagChange = function () {
        ctx.editedReleaseOptions.add('tag');
        if (ctx.createTag.value) {
            ctx.syncVersionInputs();
            ctx.scheduleReleaseNotesDraft();
        }
        else {
            ctx.releaseNotesError.value = '';
            ctx.releaseNotesLoading.value = false;
            ctx.releaseNotesRequest += 1;
        }
        ctx.setDefaultCommitMessage();
    };
    ctx.onVersionModeChange = function () {
        ctx.editedReleaseOptions.add('version');
        ctx.syncVersionInputs();
        ctx.setDefaultCommitMessage();
    };
    ctx.setTargetSelected = function (targetId: string, checked: boolean) {
        ctx.editedReleaseOptions.add('targets');
        const choice = ctx.targetChoices.value[targetId];
        const target = ctx.configuredTargets.value.find((item) => item.id === targetId);
        if (!choice || !target)
            return;
        choice.selected = checked;
        if (checked) {
            ctx.gitOnly.value = false;
            for (const phase of ctx.phaseOptions.value)
                choice[phase.key] = ctx.phaseAllowed(phase.key) && !!target.steps[phase.key];
        }
        ctx.applySyncPolicy();
    };
    ctx.setTargetPhase = function (targetId: string, phase: ExecutionPhase, checked: boolean) {
        if (phase === 'publish' && ctx.buildMode.value === 'local') {
            ctx.changeSyncPolicy(checked ? 'auto' : 'local');
            return;
        }
        ctx.editedReleaseOptions.add('targets');
        if (ctx.targetChoices.value[targetId])
            ctx.targetChoices.value[targetId][phase] = checked;
        if (checked)
            ctx.gitOnly.value = false;
    };
    ctx.versionGroupName = function (target: ReleaseTarget) {
        const group = ctx.releaseConfig.value?.versionGroups.find((item) => item.id === target.versionGroup);
        return group ? ctx.versionGroupDisplayName(group) : target.versionGroup || tr("统一版本");
    };
}
