import { computed } from 'vue';
import { tr } from '@/i18n';
import type { ReleaseContext } from './context';

// The profile is the next-operation preference. The checked request and frozen
// execution plan remain authoritative once a release has started.
export function installSync(ctx: ReleaseContext) {
    ctx.buildPlan = computed(() => ctx.buildMode.value === 'github' ? 'cloud'
        : ctx.syncPolicy.value === 'auto' ? 'local-publish'
        : ctx.localVersionMode.value === 'upgrade' ? 'local-upgrade' : 'local-current');
    // One completed card/cascade choice updates the existing preferences together.
    // Opening or cancelling the version picker never changes the execution plan.
    ctx.changeBuildPlan = function (plan) {
        if (ctx.releaseIntent.value !== 'formal' || ctx.checkingCandidate.value
            || ctx.publishing.value || ctx.autoSubmitting.value || ctx.buildPlan.value === plan) return;
        if (plan === 'local-current' || plan === 'local-upgrade')
            ctx.changeLocalVersionMode(plan === 'local-current' ? 'current' : 'upgrade');
        ctx.changeBuildMode(plan === 'cloud' ? 'github' : 'local');
        ctx.changeSyncPolicy(plan === 'cloud' || plan === 'local-publish' ? 'auto' : 'local');
    };
    ctx.localBuildOnly = computed(() => ctx.releaseIntent.value === 'formal'
        && ctx.syncPolicy.value === 'local' && ctx.localVersionMode.value === 'current');
    ctx.syncRepository = computed(() => ctx.githubRepositoryUrl(ctx.preflight.value?.remoteUrl || ''));
    ctx.syncDeliveryMissing = computed(() => ctx.syncPolicy.value === 'auto' && !!ctx.syncRepository.value
        && ctx.releaseIntent.value === 'formal' && ctx.buildMode.value === 'local'
        && ctx.chosenTargets.value.some(({ target }) => !target.delivery
            && !!(target.artifacts?.length || target.artifactRules?.length)));
    ctx.syncNotice = computed(() => {
        if (ctx.syncPolicy.value === 'local' && ctx.releaseIntent.value === 'save-progress') return tr('代码只保存在本机，不推送。');
        if (ctx.syncPolicy.value === 'local') return ctx.localBuildOnly.value
            ? tr('在本机打包，保持版本，不提交代码、不上传。')
            : tr('升级版本并在本机保存提交和 Tag，不上传。');
        if (!ctx.syncRepository.value) return tr('未检测到 GitHub 远端，请在设置中配置，或选择本机操作。');
        if (ctx.syncDeliveryMissing.value) return tr('此构建端尚未配置 GitHub 交付，请在设置中补齐，或选择“仅本地打包”。');
        if (ctx.releaseIntent.value === 'formal' && ctx.buildMode.value === 'local'
            && ctx.chosenTargets.value.some(({ target }) => target.delivery?.deployment))
            return ctx.chosenTargets.value.some(({ target }) => target.delivery?.deployment?.strategy === 'server-pull')
                ? tr('本地构建后上传 GitHub，服务器自动下载并更新；不使用 Actions 构建配额。')
                : tr('本地构建后自动上传、打包服务器镜像并更新服务器，完成后核对线上版本。');
        return ctx.releaseIntent.value === 'save-progress' ? tr('提交所选代码后自动推送到 GitHub。')
            : ctx.buildMode.value === 'local' ? tr('自动同步代码和 Tag；安装包按配置上传到 GitHub Release。')
            : tr('自动同步代码和 Tag，由 GitHub 构建和发布。');
    });
    ctx.applySyncPolicy = function () {
        // Migrate old cloud + local-only preferences to a usable local plan.
        if (ctx.releaseIntent.value === 'formal' && ctx.syncPolicy.value === 'local' && ctx.buildMode.value !== 'local') {
            ctx.localSyncPolicy.value = 'local';
            ctx.changeBuildMode('local');
            return;
        }
        ctx.pushRemote.value = ctx.syncPolicy.value === 'auto' && !!ctx.syncRepository.value;
        if (ctx.buildMode.value === 'local')
            for (const target of ctx.configuredTargets.value) {
                const choice = ctx.targetChoices.value[target.id];
                if (choice) choice.publish = ctx.pushRemote.value && !ctx.gitOnly.value && !!target.delivery;
            }
    };
    ctx.changeSyncPolicy = function (policy: 'auto' | 'local') {
        if (ctx.checkingCandidate.value || ctx.publishing.value || ctx.autoSubmitting.value || ctx.syncPolicy.value === policy)
            return;
        ctx.editedReleaseOptions.add('push');
        ctx.syncPolicy.value = policy;
        if (ctx.releaseIntent.value === 'formal') ctx.localSyncPolicy.value = policy;
        ctx.applySyncPolicy();
        ctx.rememberPreferences();
    };
    ctx.changeLocalVersionMode = function (mode: 'current' | 'upgrade') {
        if (ctx.checkingCandidate.value || ctx.publishing.value || ctx.autoSubmitting.value)
            return;
        ctx.editedReleaseOptions.add('local-version');
        ctx.localVersionMode.value = mode;
    };
}
