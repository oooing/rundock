import { computed } from 'vue';
import { tr } from '@/i18n';
import type { ReleaseContext } from './context';

// The profile is the next-operation preference. The checked request and frozen
// execution plan remain authoritative once a release has started.
export function installSync(ctx: ReleaseContext) {
    ctx.syncRepository = computed(() => ctx.githubRepositoryUrl(ctx.preflight.value?.remoteUrl || ''));
    ctx.syncDeliveryMissing = computed(() => ctx.syncPolicy.value === 'auto' && !!ctx.syncRepository.value
        && ctx.releaseIntent.value === 'formal' && ctx.buildMode.value === 'local'
        && ctx.chosenTargets.value.some(({ target }) => !target.delivery
            && !!(target.artifacts?.length || target.artifactRules?.length)));
    ctx.syncNotice = computed(() => {
        if (ctx.syncPolicy.value === 'local') return ctx.releaseIntent.value === 'formal' && ctx.buildMode.value === 'github'
            ? tr('仅本机不能使用云端构建，请切换为本地构建。') : tr('代码和安装包只保存在本机，不推送。');
        if (!ctx.syncRepository.value) return tr('未检测到 GitHub 远端，本次只保存在本机。');
        if (ctx.syncDeliveryMissing.value) return tr('此构建端尚未配置 GitHub 交付，请在设置中补齐，或选择仅保存在本机。');
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
        ctx.pushRemote.value = ctx.syncPolicy.value === 'auto' && !!ctx.syncRepository.value;
        if (ctx.buildMode.value === 'local')
            for (const target of ctx.configuredTargets.value) {
                const choice = ctx.targetChoices.value[target.id];
                if (choice) choice.publish = ctx.pushRemote.value && !ctx.gitOnly.value && !!target.delivery;
            }
    };
    ctx.changeSyncPolicy = function (policy: 'auto' | 'local') {
        ctx.editedReleaseOptions.add('push');
        ctx.syncPolicy.value = policy;
        ctx.applySyncPolicy();
        ctx.rememberPreferences();
    };
}
