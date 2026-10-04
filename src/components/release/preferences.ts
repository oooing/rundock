import { api, ApiError } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseTarget, ReleaseVersionMode } from '@/types';
import { readReleaseSession } from '@/utils/releaseSession';
import type { ExecutionPhase, ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installPreferences(ctx: ReleaseContext) {
    ctx.preferenceKey = function () {
        return `launcher.release-preferences.${ctx.props.app.id}`;
    };
    ctx.readLocalPreferences = function (): {
        buildMode?: 'github' | 'local';
        createTag?: boolean;
        versionMode?: ReleaseVersionMode;
        pushRemote?: boolean;
    } {
        try {
            return JSON.parse(localStorage.getItem(ctx.preferenceKey()) || '{}') as {
                buildMode?: 'github' | 'local';
                createTag?: boolean;
                versionMode?: ReleaseVersionMode;
                pushRemote?: boolean;
            };
        }
        catch {
            return {};
        }
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
        const key = ctx.preferenceKey();
        const preferences = JSON.stringify({ buildMode: ctx.buildMode.value, createTag: ctx.createTag.value, versionMode: ctx.versionMode.value, pushRemote: ctx.pushRemote.value });
        const revision = ctx.preferenceRevision;
        ctx.preferenceStatus.value = 'saving';
        ctx.preferenceError.value = '';
        ctx.preferenceSave = ctx.preferenceSave.catch(() => undefined).then(async () => {
            await api.saveReleaseProfile(appId, body);
            localStorage.setItem(key, preferences);
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
        if (ctx.publishing.value)
            return;
        if (ctx.preferenceTimer)
            ctx.saveRememberedPreferences();
        await ctx.preferenceSave;
        if (ctx.disposed || ctx.preferenceStatus.value === 'error')
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
    ctx.load = async function (resumeFailedRun = true) {
        ctx.loading.value = true;
        ctx.error.value = '';
        ctx.errorCode.value = '';
        ctx.versionPlanNotice.value = '';
        ctx.configNotice.value = '';
        try {
            // Handle rejection immediately while history/config load independently;
            // report the original preflight failure through the dialog below.
            const localPreflight = Promise.allSettled([api.releasePreflight(ctx.props.app.id, false)] as const);
            const [historyResult, configResult] = await Promise.allSettled([
                api.listReleases(ctx.props.app.id), api.getReleaseConfig(ctx.props.app.id),
            ] as const);
            if (ctx.disposed)
                return;
            ctx.history.value = historyResult.status === 'fulfilled' ? historyResult.value : [];
            if (configResult.status === 'fulfilled') {
                ctx.configEndpointAvailable.value = true;
                ctx.applyReleaseConfig(configResult.value);
            }
            else {
                ctx.configEndpointAvailable.value = false;
                ctx.gitOnly.value = true;
                ctx.configNotice.value = tr("当前后端暂未启用自动发布配置，仍可继续使用基础 Git 提交与 Tag 功能。");
            }
            ctx.loading.value = false;
            const saved = readReleaseSession();
            const savedRun = saved?.appId === ctx.props.app.id ? ctx.history.value.find((run) => run.id === saved.runId || (!saved.runId && saved.submittedAt &&
                Date.parse(run.createdAt.includes('T') ? run.createdAt : run.createdAt.replace(' ', 'T') + 'Z') >= saved.submittedAt - 1000)) : undefined;
            const resumable = savedRun || ctx.history.value.find((run) => run.status === 'queued' || run.status === 'running')
                || (resumeFailedRun ? ctx.history.value.find(ctx.canResumeFailedRun) : undefined);
            if (resumable)
                ctx.showRun(resumable);
            const [preflightResult] = await localPreflight;
            if (ctx.disposed)
                return;
            if (preflightResult.status === 'rejected')
                throw preflightResult.reason;
            ctx.applyPreflight(preflightResult.value, true);
            ctx.profileReady.value = true;
            if (ctx.editedReleaseOptions.size)
                ctx.rememberPreferences();
        }
        catch (reason) {
            ctx.error.value = ctx.messageOf(reason);
        }
        finally {
            ctx.profileReady.value = true;
            ctx.loading.value = false;
        }
    };
    ctx.profileBody = function () {
        return { buildMode: ctx.buildMode.value, remoteName: ctx.remoteName.value, versionStrategy: ctx.versionStrategy.value, preReleaseCommand: ctx.preReleaseCommand.value, createTag: ctx.createTag.value, versionMode: ctx.versionMode.value };
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
    };
    ctx.setTargetPhase = function (targetId: string, phase: ExecutionPhase, checked: boolean) {
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
