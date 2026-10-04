import { api } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseConfig, ReleaseFileChange, ReleasePreflight, ReleaseTarget, ReleaseTargetSteps, ReleaseVersionGroup } from '@/types';
import { reconcileReleaseSelection } from '@/utils/releaseSafety';
import { nextTick } from 'vue';
import type { ReleaseContext, ReleaseTab, TargetChoice } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installConfiguration(ctx: ReleaseContext) {
    ctx.messageOf = function (reason: unknown) {
        return reason instanceof Error ? reason.message : String(reason);
    };
    ctx.githubRepositoryUrl = function (remoteUrl: string) {
        const value = remoteUrl.trim().replace(/\.git$/i, '');
        const httpsMatch = value.match(/^https?:\/\/github\.com\/([^/]+)\/([^/]+)$/i);
        if (httpsMatch)
            return `https://github.com/${httpsMatch[1]}/${httpsMatch[2]}`;
        const sshMatch = value.match(/^(?:ssh:\/\/)?git@github\.com[:/]([^/]+)\/([^/]+)$/i);
        if (sshMatch)
            return `https://github.com/${sshMatch[1]}/${sshMatch[2]}`;
        return '';
    };
    ctx.githubActionsUrl = function (remoteUrl: string, workflow: string) {
        const repository = ctx.githubRepositoryUrl(remoteUrl);
        if (!repository)
            return '';
        return workflow ? `${repository}/actions/workflows/${encodeURIComponent(workflow)}` : `${repository}/actions`;
    };
    ctx.nextPatchVersion = function (values: string[]) {
        let best = [0, 0, 0];
        let found = false;
        for (const raw of values) {
            const match = raw.replace(/^v/, '').match(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/);
            if (!match)
                continue;
            const current = match.slice(1).map(Number);
            const isHigher = current[0] > best[0]
                || (current[0] === best[0] && current[1] > best[1])
                || (current[0] === best[0] && current[1] === best[1] && current[2] > best[2]);
            if (!found || isHigher) {
                best = current;
                found = true;
            }
        }
        if (!found)
            return '0.1.0';
        return `${best[0]}.${best[1]}.${best[2] + 1}`;
    };
    ctx.suggestReleaseVersion = function (currentVersions: string[], latestTag: string) {
        const canReleaseCurrentV2 = currentVersions.length > 0
            && currentVersions.every((version) => version.trim() === '2.0.0')
            && (() => {
                const latest = latestTag.replace(/^v/, '').match(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/);
                if (!latest)
                    return true;
                return Number(latest[1]) < 2;
            })();
        return canReleaseCurrentV2 ? '2.0.0' : ctx.nextPatchVersion([...currentVersions, latestTag]);
    };
    ctx.cloneConfig = function (config: ReleaseConfig): ReleaseConfig {
        return JSON.parse(JSON.stringify(config)) as ReleaseConfig;
    };
    ctx.normalizeConfig = function (raw: ReleaseConfig): ReleaseConfig {
        return {
            schemaVersion: 1,
            source: raw.source === 'file' ? 'file' : 'detected',
            repoRoot: raw.repoRoot || '',
            configPath: raw.configPath || '.launcher/release.yaml',
            confidence: Number.isFinite(raw.confidence) ? raw.confidence : 0,
            versionGroups: (raw.versionGroups || []).map((group) => ({
                id: group.id || 'product',
                name: group.name || group.id || tr("统一版本"),
                ...(group.tagPrefix ? { tagPrefix: group.tagPrefix } : {}),
                ...(group.currentVersion ? { currentVersion: group.currentVersion } : {}),
                versionFiles: (group.versionFiles || []).map((file) => ({
                    path: file.path || '',
                    format: file.format || 'json',
                    ...(file.jsonPointer ? { jsonPointer: file.jsonPointer } : {}),
                })),
            })),
            fileRules: raw.fileRules || [],
            checkProfiles: raw.checkProfiles || [],
            targets: (raw.targets || []).map((target) => ({
                id: target.id || `target-${Date.now()}`,
                name: target.name || target.id || tr("未命名目标"),
                kind: target.kind || 'custom',
                versionGroup: target.versionGroup || raw.versionGroups?.[0]?.id || 'product',
                workingDir: target.workingDir || '.',
                runner: { type: target.runner?.type || 'local', os: target.runner?.os || [] },
                enabled: target.enabled !== false,
                detected: target.detected !== false,
                confidence: Number.isFinite(target.confidence) ? target.confidence : 0,
                steps: {
                    check: target.steps?.check || '', build: target.steps?.build || '', package: target.steps?.package || '',
                    publish: target.steps?.publish || '', deploy: target.steps?.deploy || '',
                },
                artifacts: target.artifacts || [],
                ...(target.delivery ? { delivery: target.delivery } : {}),
                ...(target.timeouts ? { timeouts: target.timeouts } : {}),
                ...(target.artifactRules ? { artifactRules: target.artifactRules } : {}),
                ...(target.verification ? { verification: target.verification } : {}),
            })),
            ...(raw.automation ? {
                automation: {
                    ...(raw.automation.account ? { account: raw.automation.account } : {}),
                    provider: raw.automation.provider || '',
                    workflow: raw.automation.workflow || '',
                    trigger: raw.automation.trigger || 'tag',
                    releaseBranch: raw.automation.releaseBranch || '',
                    publishesRelease: raw.automation.publishesRelease === true,
                },
            } : {}),
            warnings: raw.warnings || [],
        };
    };
    ctx.currentOS = function () {
        const platform = (navigator.platform || navigator.userAgent).toLowerCase();
        if (platform.includes('win'))
            return 'windows';
        if (platform.includes('mac'))
            return 'darwin';
        return 'linux';
    };
    ctx.osLabel = function (os: string) {
        return ({ windows: 'Windows', linux: 'Linux', darwin: 'macOS' } as Record<string, string>)[os] || os;
    };
    ctx.targetUnavailableReason = function (target: ReleaseTarget) {
        if (!target.enabled)
            return tr("此目标已在配置中停用");
        const runnerType = target.runner.type.trim().toLowerCase();
        if (ctx.buildMode.value === 'github' && runnerType !== 'git-push')
            return tr('未配置 GitHub 云端构建，请配置工作流或切换本地构建');
        if (ctx.buildMode.value === 'local' && runnerType !== 'local')
            return tr('未配置本地构建步骤');
        if (ctx.buildMode.value === 'local' && !target.steps.build && !target.steps.package)
            return tr('未配置本地构建步骤');
        if (runnerType === 'git-push')
            return '';
        if (runnerType !== 'local')
            return tr("当前版本不支持此执行方式");
        if (!target.runner.os.length)
            return '';
        const supported = target.runner.os.map((value) => value.trim().toLowerCase());
        if (supported.includes('any') || supported.includes(ctx.currentOS()))
            return '';
        return tr("需要 {0} 环境，当前电脑不能执行", [target.runner.os.join(' / ')]);
    };
    ctx.targetPhaseHint = function (target: ReleaseTarget, phase: (typeof ctx.phaseOptions.value)[number]) {
        if (ctx.isTagPushTarget(target) && phase.key === 'publish')
            return tr("Tag 上传后由 GitHub 自动构建");
        if (target.runner.type.trim().toLowerCase() === 'git-push' && phase.key === 'publish')
            return tr("推送后触发云端构建");
        if (target.delivery && phase.key === 'publish')
            return tr('通过 GitHub Release 交付');
        return target.steps[phase.key] ? phase.hint : tr('不执行此步骤');
    };
    ctx.targetAvailable = function (target: ReleaseTarget) {
        return !ctx.targetUnavailableReason(target);
    };
    ctx.defaultTargetChoice = function (target: ReleaseTarget): TargetChoice {
        return { selected: false, build: ctx.phaseAllowed('build') && !!target.steps.build, package: ctx.phaseAllowed('package') && !!target.steps.package, publish: ctx.phaseAllowed('publish') && !!target.steps.publish, deploy: ctx.phaseAllowed('deploy') && !!target.steps.deploy };
    };
    ctx.applyReleaseConfig = function (raw: ReleaseConfig, editing = false) {
        const normalized = ctx.normalizeConfig(raw);
        ctx.releaseConfig.value = normalized;
        ctx.configDraft.value = ctx.cloneConfig(normalized);
        const next: Record<string, TargetChoice> = {};
        for (const target of normalized.targets)
            next[target.id] = ctx.targetChoices.value[target.id] || ctx.defaultTargetChoice(target);
        ctx.targetChoices.value = next;
        if (!normalized.targets.length)
            ctx.gitOnly.value = true;
        ctx.selectSingleBuildPlatform();
        ctx.applySyncPolicy();
        ctx.configEditorOpen.value = editing;
    };
    ctx.resetSelection = function (pf: ReleasePreflight) {
        const reconciled = reconcileReleaseSelection(pf.classifications || [], ctx.manualDecisions.value);
        ctx.selected.value = reconciled.selected;
        ctx.manualDecisions.value = reconciled.decisions;
    };
    ctx.fileStatusLabel = function (file: ReleaseFileChange) {
        if (ctx.isAddedFile(file))
            return tr("新增");
        if (file.status.includes('D'))
            return tr("删除");
        if (file.status.includes('R'))
            return tr("重命名");
        return tr("修改");
    };
    ctx.isAddedFile = function (file: ReleaseFileChange) {
        return !file.tracked || file.status.includes('A');
    };
    ctx.selectAllFiles = function (checked: boolean) {
        for (const file of ctx.preflight.value?.changes || [])
            ctx.selected.value[file.path] = checked;
    };
    ctx.normalizePreflight = function (pf: ReleasePreflight): ReleasePreflight {
        return { ...pf, remoteChecked: pf.remoteChecked !== false, remotes: pf.remotes || [], versionFiles: pf.versionFiles || [], currentVersions: pf.currentVersions || {}, latestGroupTags: pf.latestGroupTags || {}, suggestedVersions: pf.suggestedVersions || {}, changes: pf.changes || [], aheadCount: pf.aheadCount || 0, unpushedChanges: pf.unpushedChanges || [], blockingIssues: pf.blockingIssues || [] };
    };
    ctx.scanReleaseConfig = async function () {
        ctx.configScanning.value = true;
        ctx.configValidationError.value = '';
        ctx.configNotice.value = '';
        try {
            const previous = ctx.releaseConfig.value ? ctx.cloneConfig(ctx.releaseConfig.value) : null;
            ctx.applyReleaseConfig(await api.scanReleaseConfig(ctx.props.app.id), true);
            void ctx.switchReleaseTab('settings');
            ctx.configBeforeEdit.value = previous;
            ctx.configEndpointAvailable.value = true;
            ctx.configNotice.value = tr("自动识别已完成。请检查建议；点击“保存并使用”后才会写入项目。");
        }
        catch (reason) {
            if (!ctx.releaseConfig.value)
                ctx.configEndpointAvailable.value = false;
            ctx.configNotice.value = tr("自动识别暂不可用：{0}。基础 Git 发布仍可使用。", [ctx.messageOf(reason)]);
        }
        finally {
            ctx.configScanning.value = false;
        }
    };
    ctx.openConfigEditor = function () {
        if (!ctx.releaseConfig.value) {
            void ctx.scanReleaseConfig();
            return;
        }
        void ctx.switchReleaseTab('settings');
        ctx.configBeforeEdit.value = ctx.cloneConfig(ctx.releaseConfig.value);
        ctx.configDraft.value = ctx.cloneConfig(ctx.releaseConfig.value);
        ctx.configEditorOpen.value = true;
        ctx.configValidationError.value = '';
    };
    ctx.cancelConfigEdit = function () {
        if (ctx.configBeforeEdit.value)
            ctx.applyReleaseConfig(ctx.configBeforeEdit.value);
        else
            ctx.configEditorOpen.value = false;
        ctx.configBeforeEdit.value = null;
        ctx.configDraft.value = ctx.releaseConfig.value ? ctx.cloneConfig(ctx.releaseConfig.value) : null;
        ctx.configValidationError.value = '';
    };
    ctx.validateConfig = function (config: ReleaseConfig) {
        if (!config.versionGroups.length)
            return tr("至少需要一个版本组。");
        const groupIds = config.versionGroups.map((group) => group.id.trim());
        if (groupIds.some((id) => !id))
            return tr("版本组标识不能为空。");
        if (new Set(groupIds).size !== groupIds.length)
            return tr("版本组标识不能重复。");
        const tagPrefixes = config.versionGroups.map((group) => (group.tagPrefix || group.id).trim().toLowerCase());
        if (tagPrefixes.some((prefix) => !/^[a-z0-9][a-z0-9._-]*$/i.test(prefix)))
            return tr("Tag 前缀只能包含字母、数字、点、下划线和短横线。");
        if (new Set(tagPrefixes).size !== tagPrefixes.length)
            return tr("每个版本组的 Tag 前缀必须不同。");
        const targetIds = config.targets.map((target) => target.id.trim());
        if (targetIds.some((id) => !id))
            return tr("发布目标标识不能为空。");
        if (new Set(targetIds).size !== targetIds.length)
            return tr("发布目标标识不能重复。");
        const invalidTarget = config.targets.find((target) => !target.name.trim() || !groupIds.includes(target.versionGroup));
        if (invalidTarget)
            return tr("目标“{0}”缺少名称或有效版本组。", [invalidTarget.name || invalidTarget.id]);
        return '';
    };
    ctx.saveReleaseConfig = async function () {
        if (!ctx.configDraft.value)
            return;
        const validationError = ctx.validateConfig(ctx.configDraft.value);
        if (validationError) {
            ctx.configValidationError.value = validationError;
            return;
        }
        ctx.configSaving.value = true;
        ctx.configValidationError.value = '';
        ctx.configNotice.value = '';
        let savedSuccessfully = false;
        try {
            const saved = await api.saveReleaseConfig(ctx.props.app.id, ctx.normalizeConfig(ctx.configDraft.value));
            savedSuccessfully = true;
            ctx.applyReleaseConfig(saved);
            ctx.configBeforeEdit.value = null;
            ctx.configNotice.value = tr("发布说明书已保存到 {0}。", [saved.configPath || '.launcher/release.yaml']);
            ctx.preflightStale.value = true;
            ctx.applyPreflight(await api.releasePreflight(ctx.props.app.id, false));
        }
        catch (reason) {
            const message = ctx.messageOf(reason);
            ctx.configValidationError.value = message;
            if (savedSuccessfully)
                ctx.error.value = tr("配置已保存，但重新检查 Git 失败：{0}", [message]);
        }
        finally {
            ctx.configSaving.value = false;
        }
    };
    ctx.switchReleaseTab = async function (tab: ReleaseTab, focus = false) {
        if (ctx.publishing.value || ctx.autoSubmitting.value || ctx.activeRun.value)
            return;
        if (ctx.bodyRef.value)
            ctx.tabScroll[ctx.releaseTab.value] = ctx.bodyRef.value.scrollTop;
        ctx.releaseTab.value = tab;
        await nextTick();
        if (ctx.bodyRef.value)
            ctx.bodyRef.value.scrollTop = ctx.tabScroll[tab];
        if (focus)
            document.getElementById('release-tab-' + tab)?.focus();
    };
    ctx.chooseReleaseTarget = async function () {
        await ctx.switchReleaseTab('publish');
        ctx.platformSectionRef.value?.scrollIntoView({ block: 'start' });
        ctx.platformSectionRef.value?.focus({ preventScroll: true });
    };
    ctx.onReleaseTabKeydown = function (event: KeyboardEvent) {
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key))
            return;
        event.preventDefault();
        const tab = event.key === 'Home' ? 'publish' : event.key === 'End' ? 'settings' : ctx.releaseTab.value === 'publish' ? 'settings' : 'publish';
        void ctx.switchReleaseTab(tab, true);
    };
    ctx.onConfigFileSaved = async function (config: ReleaseConfig) {
        if (!ctx.configEndpointAvailable.value && config.targets.length)
            ctx.gitOnly.value = false;
        ctx.configEndpointAvailable.value = true;
        ctx.applyReleaseConfig(config);
        ctx.configBeforeEdit.value = null;
        ctx.configNotice.value = '';
        ctx.preflightStale.value = true;
        ctx.configSaving.value = true;
        try {
            ctx.applyPreflight(await api.releasePreflight(ctx.props.app.id, false));
            ctx.error.value = '';
        }
        catch (reason) {
            ctx.error.value = tr('配置已保存，但重新检查 Git 失败：{0}', [ctx.messageOf(reason)]);
        }
        finally {
            ctx.configSaving.value = false;
        }
    };
    ctx.newId = function (prefix: string, existing: string[]) {
        let index = existing.length + 1;
        while (existing.includes(`${prefix}-${index}`))
            index += 1;
        return `${prefix}-${index}`;
    };
    ctx.addVersionGroup = function () {
        if (!ctx.configDraft.value)
            return;
        const id = ctx.newId('version', ctx.configDraft.value.versionGroups.map((group) => group.id));
        ctx.configDraft.value.versionGroups.push({ id, name: tr("新版本组"), tagPrefix: id, versionFiles: [] });
    };
    ctx.removeVersionGroup = function (index: number) {
        const config = ctx.configDraft.value;
        if (!config || config.versionGroups.length <= 1)
            return;
        const [removed] = config.versionGroups.splice(index, 1);
        const fallback = config.versionGroups[0].id;
        for (const target of config.targets)
            if (target.versionGroup === removed.id)
                target.versionGroup = fallback;
    };
    ctx.addVersionFile = function (group: ReleaseVersionGroup) { group.versionFiles.push({ path: '', format: 'json', jsonPointer: '/version' }); };
    ctx.addTarget = function () {
        const config = ctx.configDraft.value;
        if (!config)
            return;
        const id = ctx.newId('target', config.targets.map((target) => target.id));
        const emptySteps: ReleaseTargetSteps = { check: '', build: '', package: '', publish: '', deploy: '' };
        config.targets.push({ id, name: tr("新发布目标"), kind: 'custom', versionGroup: config.versionGroups[0]?.id || 'product', workingDir: '.', runner: { type: 'local', os: [ctx.currentOS()] }, enabled: true, detected: false, confidence: 1, steps: emptySteps, artifacts: [] });
    };
    ctx.setRunnerOS = function (target: ReleaseTarget, os: string, checked: boolean) {
        const values = new Set(target.runner.os);
        if (checked)
            values.add(os);
        else
            values.delete(os);
        target.runner.os = [...values];
    };
    ctx.setArtifacts = function (target: ReleaseTarget, value: string) { target.artifacts = value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean); };
}
