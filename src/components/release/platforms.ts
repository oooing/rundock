import { tr } from '@/i18n';
import type { ReleaseTarget, ReleaseVersionGroup } from '@/types';
import { isAlternateBuildTarget } from '@/utils/releasePresentation';
import { computed } from 'vue';
import type { ExecutionPhase, ProductPlatform, ProductPlatformId, ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installPlatforms(ctx: ReleaseContext) {
    ctx.configConfidence = computed(() => Math.round((ctx.releaseConfig.value?.confidence || 0) * 100));
    ctx.standardPlatforms = computed<Array<{
        id: Exclude<ProductPlatformId, `custom:${string}`>;
        name: string;
        icon: string;
        description: string;
    }>>(() => ([
        { id: 'web', name: tr("Web 前端"), icon: '🌐', description: tr("网页界面") },
        { id: 'pc', name: 'PC', icon: '🖥️', description: tr("Windows 桌面端") },
        { id: 'android', name: 'Android', icon: '🤖', description: tr("Android 应用") },
        { id: 'mac', name: 'Mac', icon: '🍎', description: tr("macOS 桌面端") },
        { id: 'server', name: tr("后端服务"), icon: '🗄️', description: tr("API / 后台任务") },
    ]));
    ctx.platformIdForTarget = function (target: ReleaseTarget): ProductPlatformId | null {
        const kind = target.kind.trim().toLowerCase();
        const clue = `${target.id} ${target.name}`.toLowerCase();
        if (kind === 'node')
            return null;
        if (kind === 'desktop') {
            const systems = target.runner.os.map((value) => value.trim().toLowerCase()).filter(Boolean);
            if (systems.length === 1 && systems[0] === 'darwin')
                return 'mac';
            if (systems.length === 1 && systems[0] === 'windows')
                return 'pc';
            if (!systems.length) {
                if (clue.includes('macos') || clue.includes('mac '))
                    return 'mac';
                if (clue.includes('windows'))
                    return 'pc';
            }
            return `custom:${target.id}`;
        }
        if (kind === 'web')
            return 'web';
        if (kind === 'android')
            return 'android';
        if (['server', 'service', 'backend', 'docker'].includes(kind))
            return 'server';
        if (['mac', 'macos', 'darwin'].includes(kind) || clue.includes('macos') || clue.includes('mac '))
            return 'mac';
        if (['windows', 'pc'].includes(kind) || clue.includes('windows'))
            return 'pc';
        return `custom:${target.id}`;
    };
    ctx.versionGroupDisplayName = function (group: ReleaseVersionGroup) {
        if (group.name && !/^版本\s+\d+\.\d+\.\d+$/.test(group.name) && group.name !== '产品版本')
            return group.name;
        const platformIds = new Set(ctx.configuredTargets.value
            .filter((target) => target.versionGroup === group.id)
            .map(ctx.platformIdForTarget)
            .filter((id): id is ProductPlatformId => !!id));
        const platformNames = ctx.standardPlatforms.value.filter((platform) => platformIds.has(platform.id)).map((platform) => platform.name);
        for (const id of platformIds)
            if (id.startsWith('custom:'))
                platformNames.push(id.replace(/^custom:/, ''));
        if (platformNames.length > 1)
            return tr("{0}共用版本", [platformNames.join(' / ')]);
        if (platformNames.length === 1)
            return tr("{0}版本", [platformNames[0]]);
        return group.name || tr("项目版本");
    };
    ctx.productPlatforms = computed<ProductPlatform[]>(() => {
        const grouped = new Map<ProductPlatformId, ReleaseTarget[]>();
        for (const platform of ctx.standardPlatforms.value)
            grouped.set(platform.id, []);
        for (const target of ctx.configuredTargets.value) {
            const platformId = ctx.platformIdForTarget(target);
            if (!platformId)
                continue;
            const targets = grouped.get(platformId) || [];
            targets.push(target);
            grouped.set(platformId, targets);
        }
        const cards: ProductPlatform[] = ctx.standardPlatforms.value.map((platform) => ({
            ...platform,
            // Keep configured combined targets (e.g. Web + backend) visible by name.
            name: grouped.get(platform.id)?.length === 1 ? grouped.get(platform.id)![0].name || platform.name : platform.name,
            targets: grouped.get(platform.id) || [],
            configured: !!grouped.get(platform.id)?.length,
        }));
        for (const [id, targets] of grouped) {
            if (!id.startsWith('custom:'))
                continue;
            const target = targets[0];
            const isDesktop = target?.kind.trim().toLowerCase() === 'desktop';
            cards.push({ id, name: target?.name || (isDesktop ? tr("桌面端") : tr("自定义目标")), icon: isDesktop ? '💻' : '🧩', description: tr("自定义发布目标"), targets, configured: true });
        }
        // Unconfigured placeholders are not selectable build targets. Keep configured
        // but unavailable targets so their mode/environment explanation remains visible.
        return cards.filter((platform) => platform.configured);
    });
    ctx.visibleProductPlatforms = computed(() => ctx.productPlatforms.value.filter(platform => ctx.platformRunnableTargets(platform).length || !platform.targets.every(target => ctx.configuredTargets.value.some(other => isAlternateBuildTarget(target, other) &&
        ctx.targetAvailable(other) && ctx.configuredActions(other).length > 0))));
    ctx.phaseAllowed = function (phase: ExecutionPhase) {
        return ctx.buildMode.value === 'github' ? phase === 'publish' : phase === 'build' || phase === 'package' || phase === 'publish';
    };
    ctx.selectedDelivery = computed(() => ctx.chosenTargets.value.some(({ target, choice }) => !!target.delivery && choice.publish));
    ctx.changeBuildMode = function (mode: 'github' | 'local') {
        ctx.editedReleaseOptions.add('build');
        if (ctx.buildMode.value === mode)
            return;
        const platforms = new Set(ctx.productPlatforms.value.filter(ctx.platformHasSelection).map(platform => platform.id));
        ctx.buildMode.value = mode;
        for (const target of ctx.configuredTargets.value)
            ctx.targetChoices.value[target.id] = ctx.defaultTargetChoice(target);
        if (!ctx.gitOnly.value)
            for (const platform of ctx.productPlatforms.value) {
                if (platforms.has(platform.id))
                    ctx.togglePlatform(platform, true);
            }
        ctx.applySyncPolicy();
    };
    ctx.configuredActions = function (target: ReleaseTarget) {
        return ctx.phaseOptions.value.filter((phase) => ctx.phaseAllowed(phase.key) && !!target.steps[phase.key]);
    };
    ctx.platformRunnableTargets = function (platform: ProductPlatform) {
        return platform.targets.filter((target) => ctx.targetAvailable(target) && ctx.configuredActions(target).length > 0);
    };
    ctx.platformSelected = function (platform: ProductPlatform) {
        const runnable = ctx.platformRunnableTargets(platform);
        return runnable.length > 0 && runnable.every((target) => ctx.targetChoices.value[target.id]?.selected);
    };
    ctx.platformHasSelection = function (platform: ProductPlatform) {
        return ctx.platformRunnableTargets(platform).some((target) => ctx.targetChoices.value[target.id]?.selected);
    };
    ctx.platformPartiallySelected = function (platform: ProductPlatform) {
        const count = ctx.platformSelectionCount(platform);
        return count.selected > 0 && count.selected < count.runnable;
    };
    ctx.platformPartiallyAvailable = function (platform: ProductPlatform) {
        const count = ctx.platformSelectionCount(platform);
        return count.runnable > 0 && count.runnable < count.total;
    };
    ctx.platformSelectionCount = function (platform: ProductPlatform) {
        const runnable = ctx.platformRunnableTargets(platform);
        return {
            selected: runnable.filter((target) => ctx.targetChoices.value[target.id]?.selected).length,
            runnable: runnable.length,
            total: platform.targets.filter(target => target.runner.type.trim().toLowerCase() === (ctx.buildMode.value === 'github' ? 'git-push' : 'local')).length,
        };
    };
    ctx.platformUnavailableReason = function (platform: ProductPlatform) {
        if (!platform.configured) {
            if (ctx.buildMode.value === 'local' && platform.id === 'mac')
                return ctx.currentOS() === 'darwin' ? tr("未配置 Mac 构建") : tr("未配置，且需在 macOS 电脑运行");
            return tr("未识别到此平台");
        }
        const runnable = ctx.platformRunnableTargets(platform);
        if (runnable.length)
            return '';
        if (ctx.buildMode.value === 'local' && platform.id === 'mac') {
            const needsMac = platform.targets.some((target) => {
                const systems = target.runner.os.map((value) => value.trim().toLowerCase());
                return target.runner.type.trim().toLowerCase() === 'local' && systems.includes('darwin') && !systems.includes('any');
            });
            if (needsMac && ctx.currentOS() !== 'darwin')
                return tr("需在 macOS 电脑运行");
        }
        const environmentReason = platform.targets.map(ctx.targetUnavailableReason).find(Boolean);
        if (environmentReason)
            return environmentReason;
        return tr("尚未配置可执行的构建或发布动作");
    };
    ctx.platformActionLabels = function (platform: ProductPlatform, selectedOnly = false) {
        const targetIds = new Set(platform.targets.map((target) => target.id));
        return ctx.phaseOptions.value
            .filter((phase) => selectedOnly
            ? ctx.selectedTargets.value.some((target) => targetIds.has(target.targetId) && target[phase.key])
            : ctx.platformRunnableTargets(platform).some((target) => ctx.phaseAllowed(phase.key) && !!target.steps[phase.key]))
            .map((phase) => phase.label);
    };
    ctx.platformCardDetail = function (platform: ProductPlatform) {
        const unavailableReason = ctx.platformUnavailableReason(platform);
        if (unavailableReason)
            return unavailableReason;
        if (ctx.platformPartiallySelected(platform)) {
            const count = ctx.platformSelectionCount(platform);
            return tr("{0} · 已选 {1}/{2}，点击全选", [platform.description, count.selected, count.runnable]);
        }
        if (ctx.platformPartiallyAvailable(platform))
            return tr("部分步骤不可用");
        return platform.description === tr('自定义发布目标') ? '' : platform.description;
    };
    ctx.togglePlatform = function (platform: ProductPlatform, checked: boolean) {
        ctx.editedReleaseOptions.add('targets');
        if (checked)
            ctx.gitOnly.value = false;
        for (const target of ctx.platformRunnableTargets(platform)) {
            const choice = ctx.targetChoices.value[target.id];
            if (!choice)
                continue;
            choice.selected = checked;
            if (checked) {
                for (const phase of ctx.phaseOptions.value)
                    choice[phase.key] = ctx.phaseAllowed(phase.key) && !!target.steps[phase.key];
            }
        }
        ctx.applySyncPolicy();
    };
    ctx.selectSingleBuildPlatform = function (enteringRelease = false) {
        if (ctx.gitOnly.value || (!enteringRelease && ctx.editedReleaseOptions.has('targets')))
            return;
        if (Object.values(ctx.targetChoices.value).some(choice => choice.selected))
            return;
        const available = ctx.productPlatforms.value.filter(platform => ctx.platformRunnableTargets(platform).length > 0);
        if (available.length !== 1)
            return;
        for (const target of ctx.platformRunnableTargets(available[0])) {
            const choice = ctx.targetChoices.value[target.id];
            if (choice)
                choice.selected = true;
        }
    };
    ctx.toggleGitOnly = function (checked: boolean) {
        ctx.changeReleaseIntent(checked ? 'save-progress' : 'formal');
    };
    ctx.stageLabel = computed<Record<string, string>>(() => ({
        preparing: tr("准备发布"), versioning: tr("更新版本"), checking: tr("发布前检查"), committing: tr("创建提交"),
        building_targets: tr("检查、构建和打包"), publishing_targets: tr("上传和部署"),
        target_check: tr("检查目标"), target_build: tr("构建目标"), target_package: tr("打包目标"),
        target_publish: tr("上传目标"), target_deploy: tr("部署目标"), tagging: tr("创建 Tag"),
        pushing_branch: tr("推送分支"), pushing_tag: tr("推送 Tag"), completed: tr("发布完成"),
        delivery_preparing: tr("验证并保存产物"), delivery_preflight: tr("核对交付条件"), delivery_publish: tr("上传并核验 GitHub Release"),
    }));
    ctx.targetStageLabel = computed<Record<string, string>>(() => ({
        waiting: tr("等待执行"), checking: tr("检查"), check: tr("检查"), build: tr("构建"), package: tr("打包"),
        ready_to_publish: tr("等待上传或部署"), waiting_publish: tr("等待上传或部署"), publish: tr("上传"),
        deploy: tr("部署"), artifacts: tr("核对产物"), triggered: tr("已触发云端流程"), remote_pending: tr("等待云端处理"),
        cloud_pending: tr('云端结果待确认'), completed: tr("已完成"),
    }));
    ctx.summaryLines = computed(() => {
        if (ctx.targetSelectionMissing.value)
            return [];
        const lines: string[] = [];
        if (ctx.createTag.value) {
            for (const version of ctx.plannedVersions.value)
                lines.push(tr("{0}：{1}（{2}）", [version.versionGroupName, version.targetVersion || tr("待填写"), version.tagName || tr("待生成 Tag")]));
        }
        else
            lines.push(tr("不创建版本 Tag"));
        const fileCount = ctx.selectedPaths.value.length;
        if (!fileCount && !ctx.createTag.value)
            lines.push(ctx.pushRemote.value ? tr("上传当前分支") : tr("不创建新提交"));
        else if (!fileCount)
            lines.push(ctx.pushRemote.value ? tr("创建并上传 Tag") : tr("创建本地 Tag"));
        if (ctx.pushRemote.value) {
            lines.push(tr("提交后上传到{0}", [ctx.remoteDestination.value]));
            if (ctx.preflight.value?.aheadCount)
                lines.push(tr("同时上传本机已有的 {0} 次提交", [ctx.preflight.value.aheadCount]));
        }
        else {
            lines.push(tr("只保存在本机，不上传远程仓库"));
        }
        if (ctx.gitOnly.value) {
            lines.push(tr("不构建平台"));
        }
        else {
            for (const platform of ctx.productPlatforms.value.filter(ctx.platformHasSelection)) {
                const partial = ctx.platformPartiallySelected(platform) ? tr("（部分目标）") : '';
                const cloudBuild = platform.targets.some((target) => ctx.isTagPushTarget(target) && ctx.targetChoices.value[target.id]?.selected);
                const actions = ctx.platformActionLabels(platform, true).filter((label) => !cloudBuild || label !== tr("上传"));
                if (cloudBuild)
                    actions.unshift(tr("交给 GitHub 自动构建"));
                lines.push(tr("{0}{1}：{2}", [platform.name, partial, actions.join('、') || tr("尚未选择执行动作")]));
            }
            const advancedTargets = ctx.chosenTargets.value.filter(({ target }) => ctx.platformIdForTarget(target) === null);
            if (advancedTargets.length)
                lines.push(tr("高级目标：{0}", [advancedTargets.map(({ target }) => target.name).join('、')]));
        }
        if (ctx.willTriggerAutomation.value) {
            lines.push(ctx.willBuildTargetsInAutomation.value
                ? tr("Tag 上传后交给 GitHub 自动构建；完成后可在 GitHub 查看结果")
                : tr("Tag 上传后创建源码 Release，不生成安装包"));
        }
        return lines;
    });
}
