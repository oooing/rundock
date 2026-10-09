import { api } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseRun, ReleaseTarget, ReleaseVersionGroup, SelectedReleaseTarget } from '@/types';
import { releaseContentState } from '@/utils/releaseContent';
import { computed } from 'vue';
import type { PlannedVersion, ProductPlatform, ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installSelection(ctx: ReleaseContext) {
    ctx.isActive = computed(() => ['starting', 'running', 'degraded', 'stopping'].includes(ctx.props.app.status));
    ctx.selectedPaths = computed(() => Object.entries(ctx.selected.value).filter(([, value]) => value).map(([path]) => path));
    ctx.orderedChanges = computed(() => {
        const changes = ctx.preflight.value?.changes || [];
        return [...changes.filter(ctx.isAddedFile), ...changes.filter((file) => !ctx.isAddedFile(file))];
    });
    ctx.newFiles = computed(() => ctx.preflight.value?.changes.filter(ctx.isAddedFile) || []);
    ctx.allFilesSelected = computed(() => !!ctx.preflight.value?.changes.length && ctx.preflight.value.changes.every((file) => ctx.selected.value[file.path]));
    ctx.configuredTargets = computed(() => ctx.releaseConfig.value?.targets || []);
    ctx.chosenTargets = computed(() => ctx.gitOnly.value ? [] : ctx.configuredTargets.value
        .filter((target) => ctx.targetChoices.value[target.id]?.selected && ctx.targetAvailable(target))
        .map((target) => ({ target, choice: ctx.targetChoices.value[target.id] })));
    ctx.selectedTargets = computed<SelectedReleaseTarget[]>(() => ctx.gitOnly.value ? [] : ctx.chosenTargets.value
        .map(({ target, choice }) => {
        return {
            targetId: target.id,
            build: ctx.phaseAllowed('build') && !!target.steps.build && !!choice.build,
            package: ctx.phaseAllowed('package') && !!target.steps.package && !!choice.package,
            publish: ctx.phaseAllowed('publish') && !!(target.delivery || target.steps.publish) && !!choice.publish,
            deploy: ctx.phaseAllowed('deploy') && !!target.steps.deploy && !!choice.deploy,
        };
    }));
    ctx.invalidChosenTargetIds = computed(() => ctx.selectedTargets.value
        .filter((target) => !target.build && !target.package && !target.publish && !target.deploy)
        .map((target) => target.targetId));
    ctx.targetSelectionMissing = computed(() => !ctx.gitOnly.value && !ctx.selectedTargets.value.length);
    ctx.selectedVersionGroupIds = computed(() => [...new Set(ctx.chosenTargets.value.map(({ target }) => target.versionGroup))]);
    ctx.selectedVersionGroups = computed(() => (ctx.releaseConfig.value?.versionGroups || [])
        .filter((group) => ctx.selectedVersionGroupIds.value.includes(group.id)));
    ctx.selectedVersionFiles = computed(() => {
        if (ctx.gitOnly.value || !ctx.selectedVersionGroups.value.length)
            return ctx.preflight.value?.versionFiles || [];
        return [...new Set(ctx.selectedVersionGroups.value.flatMap((group) => group.versionFiles.map((file) => file.path)))];
    });
    ctx.visibleCurrentVersions = computed<Record<string, string>>(() => {
        const values: Record<string, string> = {};
        if (ctx.gitOnly.value || !ctx.selectedVersionGroups.value.length) {
            for (const path of ctx.preflight.value?.versionFiles || [])
                values[path] = ctx.preflight.value?.currentVersions[path] || '';
            return values;
        }
        for (const group of ctx.selectedVersionGroups.value) {
            if (!group.versionFiles.length && group.currentVersion)
                values[group.name] = group.currentVersion;
            for (const file of group.versionFiles)
                values[file.path] = ctx.preflight.value?.currentVersions[file.path] || group.currentVersion || '';
        }
        return values;
    });
    ctx.isTagPushTarget = function (target: ReleaseTarget) {
        return target.runner.type.trim().toLowerCase() === 'git-push'
            && ((target.steps.publish || '').trim().toLowerCase() === 'tag-push' || (target.steps.publish || '').startsWith('workflow-dispatch:'));
    };
    ctx.plannedVersions = computed<PlannedVersion[]>(() => {
        const pf = ctx.preflight.value;
        if (!pf || !ctx.createTag.value)
            return [];
        if (ctx.gitOnly.value || !ctx.selectedVersionGroups.value.length) {
            const suggestedVersion = pf.suggestedVersion || '0.1.0';
            const target = ctx.versionMode.value === 'auto' ? suggestedVersion : (ctx.versionInputs.value.repository || suggestedVersion);
            return [{ versionGroupId: 'repository', versionGroupName: tr("项目版本"), currentVersion: pf.latestTag || tr("未创建 Tag"), suggestedVersion, targetVersion: target, tagName: `v${target}` }];
        }
        return ctx.selectedVersionGroups.value.map(ctx.versionForGroup);
    });
    ctx.versionForGroup = function (group: ReleaseVersionGroup): PlannedVersion {
        const pf = ctx.preflight.value!;
        const namespaced = (ctx.releaseConfig.value?.versionGroups.length || 0) > 1;
        const values: string[] = [];
        if (!group.versionFiles.length && group.currentVersion)
            values.push(group.currentVersion);
        for (const file of group.versionFiles)
            values.push(pf.currentVersions[file.path] || group.currentVersion || '');
        const latestGroupVersion = (pf.latestGroupTags[group.id] || (!namespaced ? pf.latestTag : '') || '').replace(/^.*\/v|^v/, '');
        const suggestedVersion = namespaced
            ? (pf.suggestedVersions[group.id] || ctx.nextPatchVersion([...values, latestGroupVersion]))
            : ctx.suggestReleaseVersion(values.filter(Boolean), pf.latestTag);
        const target = ctx.versionMode.value === 'auto' ? suggestedVersion : (ctx.versionInputs.value[group.id] || suggestedVersion);
        const prefix = group.tagPrefix || group.id;
        return {
            versionGroupId: group.id,
            versionGroupName: ctx.versionGroupDisplayName(group),
            currentVersion: latestGroupVersion || values.find((value) => /^(?:v)?\d+\.\d+\.\d+$/.test(value))?.replace(/^v/, '') || tr("未识别"),
            suggestedVersion,
            targetVersion: target,
            tagName: namespaced ? `${prefix}/v${target}` : `v${target}`,
        };
    };
    ctx.platformVersions = function (platform: ProductPlatform) {
        if (!ctx.preflight.value)
            return [];
        const groupIds = new Set(platform.targets.map(target => target.versionGroup));
        return (ctx.releaseConfig.value?.versionGroups || []).filter(group => groupIds.has(group.id)).map(ctx.versionForGroup);
    };
    ctx.displayCurrentVersion = function (value: string) {
        return /^(?:v)?\d+\.\d+\.\d+$/.test(value) ? `v${value.replace(/^v/, '')}` : value;
    };
    ctx.versionValid = computed(() => !ctx.createTag.value || (ctx.plannedVersions.value.length > 0 && ctx.plannedVersions.value.every((version) => ctx.versionPattern.test(version.targetVersion))));
    ctx.primaryTargetVersion = computed(() => ctx.plannedVersions.value[0]?.targetVersion || '');
    ctx.plannedTagNames = computed(() => ctx.plannedVersions.value.map((version) => version.tagName));
    ctx.configNeedsSaving = computed(() => !!ctx.releaseConfig.value?.targets.length && ctx.releaseConfig.value.source !== 'file');
    ctx.selectedNeedsRemotePush = computed(() => ctx.chosenTargets.value.some(({ target, choice }) => target.runner.type.trim().toLowerCase() === 'git-push' || (!!target.delivery && choice.publish)));
    ctx.configuredAutomation = computed(() => {
        const automation = ctx.releaseConfig.value?.automation;
        return automation?.provider.trim().toLowerCase() === 'github-actions' ? automation : null;
    });
    ctx.selectedHasTagPushTarget = computed(() => ctx.chosenTargets.value.some(({ target }) => ctx.isTagPushTarget(target)));
    ctx.automationTargetRequiresTag = computed(() => ctx.selectedHasTagPushTarget.value
        || (!!ctx.configuredAutomation.value
            && ctx.configuredAutomation.value.trigger.trim().toLowerCase() === 'tag'
            && ctx.selectedNeedsRemotePush.value));
    ctx.willTriggerAutomation = computed(() => (ctx.selectedHasTagPushTarget.value
        || (!!ctx.configuredAutomation.value
            && ctx.configuredAutomation.value.trigger.trim().toLowerCase() === 'tag'
            && ctx.configuredAutomation.value.publishesRelease))
        && ctx.createTag.value
        && ctx.pushRemote.value);
    ctx.automationBranchMismatch = computed(() => !!ctx.willTriggerAutomation.value
        && !!ctx.configuredAutomation.value?.releaseBranch
        && ctx.configuredAutomation.value.releaseBranch !== ctx.preflight.value?.branch);
    ctx.willBuildWindowsInAutomation = computed(() => ctx.buildMode.value === 'github' && ctx.productPlatforms.value.some((platform) => platform.id === 'pc' && ctx.platformHasSelection(platform)));
    ctx.willBuildTargetsInAutomation = computed(() => ctx.selectedHasTagPushTarget.value || ctx.willBuildWindowsInAutomation.value);
    ctx.releaseNotesOptionsSignature = computed(() => JSON.stringify({
        gitOnly: ctx.gitOnly.value,
        statusFingerprint: ctx.preflight.value?.statusFingerprint || '',
        selectedPaths: [...ctx.selectedPaths.value].sort(),
        selectedTargets: [...ctx.selectedTargets.value].sort((left, right) => left.targetId.localeCompare(right.targetId)),
        createTag: ctx.createTag.value,
        versions: ctx.plannedVersions.value.map((version) => `${version.versionGroupId}:${version.tagName}`),
    }));
    ctx.targetSelectionValid = computed(() => !ctx.syncDeliveryMissing.value && (!ctx.selectedDelivery.value || (ctx.createTag.value && ctx.pushRemote.value)) && (ctx.gitOnly.value
        || (ctx.selectedTargets.value.length > 0 && ctx.invalidChosenTargetIds.value.length === 0))
        && (ctx.pushRemote.value || !ctx.selectedNeedsRemotePush.value)
        && (ctx.createTag.value || !ctx.automationTargetRequiresTag.value)
        && !ctx.automationBranchMismatch.value);
    ctx.newContentState = computed(() => {
        const pf = ctx.preflight.value;
        if (!pf)
            return 'unknown';
        const namespaced = (ctx.releaseConfig.value?.versionGroups.length || 0) > 1;
        const baseTags = ctx.plannedVersions.value.map(version => namespaced && version.versionGroupId !== 'repository'
            ? pf.latestGroupTags[version.versionGroupId] || '' : pf.latestTag || '');
        return releaseContentState(ctx.createTag.value, ctx.selectedPaths.value, pf.changes.map(change => change.path), baseTags, pf.commitsSinceTags);
    });
    ctx.releaseContentHint = computed(() => ctx.newContentState.value === 'none'
        ? tr('暂无新内容，无需发布新版本')
        : ctx.newContentState.value === 'unknown' && ctx.preflight.value && !ctx.loading.value
            ? tr('无法确认版本后的改动，请刷新发布检查；旧版后端需先更新') : '');
    ctx.blockingIssues = computed(() => (ctx.preflight.value?.blockingIssues || []).filter(issue => {
        if (!ctx.pushRemote.value && (issue.code.startsWith('remote_') || ['fetch_failed', 'branch_behind'].includes(issue.code)))
            return false;
        if (ctx.gitOnly.value && !ctx.createTag.value && ['release_config_invalid', 'version_file_invalid', 'version_file_ignored', 'diagnostics_version_file_untracked'].includes(issue.code))
            return false;
        return true;
    }));
    ctx.localChecksPassed = computed(() => !!ctx.preflight.value && !ctx.blockingIssues.value.length
        && (ctx.preflight.value.canRelease || ctx.preflight.value.blockingIssues.length > 0));
    ctx.remoteMissing = computed(() => ctx.pushRemote.value && !!ctx.preflight.value && !ctx.preflight.value.remotes.includes(ctx.remoteName.value));
    ctx.canSubmit = computed(() => {
        if (ctx.ignoringFile.value) return false;
        // Current-version packaging uses the isolated local-build API, never release/commit.
        if (ctx.localBuildOnly.value || (ctx.syncPolicy.value === 'auto' && !ctx.syncRepository.value))
            return false;
        if (ctx.preferenceStatus.value === 'saving' || ctx.preferenceStatus.value === 'error')
            return false;
        if (ctx.safetySettingsDirty.value)
            return false;
        if (ctx.unstaging.value || ctx.configFileDirty.value || ctx.configEditorOpen.value || ctx.configSaving.value || ctx.configScanning.value)
            return false;
        if (!ctx.localChecksPassed.value || ctx.remoteMissing.value || ctx.savingProfile.value || ctx.preflightStale.value || ctx.activeRun.value || ctx.publishing.value || !ctx.commitMessage.value.trim())
            return false;
        if (ctx.createTag.value && (!ctx.versionValid.value || ctx.releaseNotesLoading.value || (ctx.releaseNotesStale.value && !ctx.releaseNotesDirty.value) || !ctx.releaseNotes.value.trim()))
            return false;
        if (ctx.newContentState.value !== 'new')
            return false;
        return ctx.targetSelectionValid.value;
    });
    ctx.canPublish = computed(() => ctx.candidateReady.value && ctx.canSubmit.value);
    ctx.canRetryRun = function (run: ReleaseRun | null | undefined) {
        return !!run && run.status === 'failed' && !!run.commitSha && run.errorCode !== 'build_changed_tree' && [
            'tagging', 'pushing_branch', 'pushing_tag', 'building_targets', 'publishing_targets',
            'target_check', 'target_build', 'target_package', 'target_publish', 'target_deploy',
            'delivery_preflight', 'delivery_publish',
        ].includes(run.stage);
    };
    ctx.retryable = computed(() => ctx.canRetryRun(ctx.activeRun.value));
    ctx.customRetryConfirmation = computed(() => ctx.retryConfirmationRequired.value ?? (!!ctx.activeRun.value
        && ['publishing_targets', 'target_publish', 'target_deploy'].includes(ctx.activeRun.value.stage)));
    ctx.retryUpload = computed(() => !!ctx.activeRun.value?.pushRemote
        && ['pushing_branch', 'pushing_tag', 'delivery_preflight', 'delivery_publish'].includes(ctx.activeRun.value.stage));
    ctx.uploadPaused = computed(() => ctx.retryUpload.value && ctx.activeRun.value?.status === 'failed' && !!ctx.activeRun.value.commitSha);
    ctx.runFailureSummary = computed(() => ctx.activeRun.value?.errorMessage?.split('\n')[0] || '');
    ctx.runFailureDetails = computed(() => ctx.activeRun.value?.errorMessage?.split('\n').slice(1).join('\n').trim() || '');
    ctx.retryButtonLabel = computed(() => ctx.retrying.value ? tr('正在重试…')
        : ctx.retryUpload.value ? tr('重试上传')
            : ctx.activeRun.value?.stage === 'target_deploy' ? tr('重试部署')
                : ['building_targets', 'target_check', 'target_build', 'target_package'].includes(ctx.activeRun.value?.stage || '') ? tr('重试构建')
                    : tr('继续发布'));
    ctx.retryGuidance = computed(() => ctx.retryUpload.value
        ? tr('本地提交和版本已保留。重试时会自动检查上传结果，跳过已上传的内容，继续未完成的步骤。')
        : tr('使用本次发布记录继续处理未完成的步骤。'));
    ctx.retryTargetNames = computed(() => ctx.retryConfirmationTargets.value.length
        ? ctx.retryConfirmationTargets.value.join('、') : tr('本次选择的发布目标'));
    ctx.hasOnlineAction = computed(() => ctx.selectedTargets.value.some((target) => target.publish || target.deploy));
    ctx.hasExternalAction = computed(() => ctx.hasOnlineAction.value || ctx.willTriggerAutomation.value);
    ctx.remoteDestination = computed(() => /github\.com/i.test(ctx.preflight.value?.remoteUrl || '') ? 'GitHub' : tr("远程仓库"));
    ctx.automationPageUrl = computed(() => ctx.cloudBuild.value?.url || ctx.runAutomation.value?.url
        || ctx.activeRun.value?.automationUrl
        || ctx.githubActionsUrl(ctx.preflight.value?.remoteUrl || '', ctx.configuredAutomation.value?.workflow || ''));
    ctx.openAutomationPage = async function () {
        const url = ctx.automationPageUrl.value;
        if (!url || ctx.openingAutomation.value)
            return;
        ctx.openingAutomation.value = true;
        ctx.automationOpenError.value = '';
        try {
            await api.openURL(ctx.props.app.id, url);
        }
        catch (reason) {
            ctx.automationOpenError.value = tr('未能打开浏览器：{0}', [ctx.messageOf(reason)]);
        }
        finally {
            ctx.openingAutomation.value = false;
        }
    };
    // Completed run state comes from its frozen backend view, never today's config.
    ctx.automationHandedOff = computed(() => !!ctx.activeRun.value?.pushRemote
        && ctx.runDeliveries.value.length === 0
        && (ctx.runTargets.value.some(target => ['triggered', 'remote_pending', 'handed_off'].includes(target.status))
            || !!ctx.runAutomation.value || !!ctx.activeRun.value.automationUrl));
    ctx.activeRunStatusLabel = computed(() => {
        if (ctx.activeRun.value?.status === 'failed')
            return tr("失败");
        if (ctx.activeRun.value?.status !== 'succeeded')
            return tr("进行中");
        return ctx.automationHandedOff.value ? tr("已提交 GitHub") : tr("成功");
    });
    ctx.activeStageLabel = computed(() => {
        if (!ctx.activeRun.value)
            return '';
        if (ctx.activeRun.value.stage === 'completed' && ctx.automationHandedOff.value)
            return tr("本地发布完成，等待 GitHub 自动处理");
        return ctx.stageLabel.value[ctx.activeRun.value.stage] || ctx.activeRun.value.stage;
    });
    ctx.cloudExecutionNotice = computed(() => {
        if (ctx.activeRun.value ? !ctx.automationHandedOff.value : !ctx.willTriggerAutomation.value)
            return null;
        const selections = ctx.activeRun.value?.selectedTargets || ctx.selectedTargets.value;
        if (!selections.length)
            return {
                title: tr("GitHub Actions 发布源码版本"),
                text: tr("代码和 Tag 上传后，由 GitHub Actions 创建源码版本，不生成安装包。"),
            };
        const allCloud = selections.every((selection) => ctx.configuredTargets.value
            .find((target) => target.id === selection.targetId)?.runner.type.trim().toLowerCase() === 'git-push');
        return {
            title: tr("GitHub Actions 云端构建与发布"),
            text: allCloud
                ? tr("本机不构建、不打包。构建、打包和发布由 GitHub Actions 按项目配置执行。")
                : tr("云端目标由 GitHub Actions 构建、打包并按项目配置发布；本地目标仍按配置执行。"),
        };
    });
    ctx.cloudBuildSettled = computed(() => ctx.cloudBuild.value?.state === 'succeeded' || ctx.cloudBuild.value?.state === 'superseded');
    ctx.completionTitle = computed(() => ctx.runDeliveries.value.length && ctx.runDeliveries.value.every(item => item.state === 'published') ? tr('构建与发布已完成') : ctx.cloudBuild.value?.state === 'superseded' ? tr('旧版本已由新构建替代') : ctx.cloudBuild.value?.state === 'failed' ? tr('云端构建失败') : ctx.cloudBuild.value?.state === 'succeeded' ? tr('云端构建已完成') : ctx.automationHandedOff.value ? tr('代码已上传，正在跟踪云端构建') : ctx.activeRun.value?.pushRemote
        ? tr("已提交到 {0}", [ctx.automationHandedOff.value ? 'GitHub' : ctx.remoteDestination.value])
        : tr("本地操作已完成"));
    ctx.completionDescription = computed(() => {
        if (ctx.runDeliveries.value.length)
            return tr('安装包已保存；发布结果和服务器同步状态分别显示。');
        if (ctx.cloudBuild.value?.summary)
            return tr(ctx.cloudBuild.value.summary);
        if (!ctx.activeRun.value?.pushRemote)
            return tr("提交已保存在本机，尚未上传到远程仓库。");
        return ctx.activeRun.value.createTag ? tr("代码和版本 Tag 已上传。") : tr("代码已上传。");
    });
    ctx.confirmDialogTitle = computed(() => {
        if (ctx.confirmAction.value === 'regenerate-notes')
            return tr("重新生成更新说明？");
        return tr('重新执行发布或部署命令？');
    });
    ctx.confirmDialogMessage = computed(() => {
        if (ctx.confirmAction.value === 'regenerate-notes')
            return tr("重新生成会覆盖你手动修改的内容。");
        return tr('这会重新执行“{0}”的发布或部署命令，可能再次更新线上服务。程序无法自动判断自定义命令上次是否已完成。', [ctx.retryTargetNames.value]);
    });
    ctx.confirmDialogButton = computed(() => ctx.confirmAction.value === 'regenerate-notes' ? tr("覆盖并生成") : tr('重新执行'));
}
