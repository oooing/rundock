import { rememberReleaseSession } from '@/utils/releaseSession';
import { nextTick, onBeforeUnmount, onMounted, watch } from 'vue';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installLifecycle(ctx: ReleaseContext) {
    watch([ctx.releaseIntent, ctx.gitOnly, ctx.createTag], () => {
        ctx.gitOnly.value = ctx.releaseIntent.value === 'save-progress';
        ctx.createTag.value = ctx.releaseIntent.value === 'formal';
    }, { flush: 'sync' });
    watch([ctx.buildMode, ctx.createTag, ctx.versionMode, ctx.pushRemote], ctx.rememberPreferences);
    watch(ctx.gitOnly, value => {
        if (!value && ctx.buildMode.value === 'local')
            ctx.pushRemote.value = false;
    });
    watch(ctx.pushRemote, () => {
        if (ctx.errorCode.value.startsWith('remote_') || ctx.errorCode.value === 'fetch_failed') {
            ctx.error.value = '';
            ctx.errorCode.value = '';
        }
    });
    watch([() => ctx.activeRun.value?.id, () => ctx.activeRun.value?.status], async () => {
        await nextTick();
        ctx.bodyRef.value?.scrollTo({ top: 0 });
    });
    watch(ctx.releaseNotesOptionsSignature, (signature) => {
        if (!ctx.createTag.value)
            return;
        if (ctx.releaseNotesGeneratedFor.value && signature === ctx.releaseNotesGeneratedFor.value)
            ctx.releaseNotesStale.value = false;
        else {
            ctx.releaseNotesStale.value = !!ctx.releaseNotesGeneratedFor.value;
            if (!ctx.releaseNotesDirty.value)
                ctx.scheduleReleaseNotesDraft();
        }
    });
    watch(ctx.plannedTagNames, (tags) => {
        if (ctx.profileReady.value && ctx.createTag.value && ctx.versionMode.value === 'auto' && tags.length) {
            ctx.syncVersionInputs();
            ctx.setDefaultCommitMessage();
        }
    });
    onMounted(() => {
        ctx.disposed = false;
        void ctx.load();
    });
    onBeforeUnmount(() => {
        ctx.candidateAbort?.abort();
        ctx.disposed = true;
        if (ctx.checkingCandidate.value)
            void ctx.cancelCandidate();
        if (ctx.candidatePoll)
            clearTimeout(ctx.candidatePoll);
        if (ctx.activeRun.value?.status === 'succeeded')
            rememberReleaseSession({ appId: ctx.props.app.id });
        if (ctx.pollTimer)
            clearTimeout(ctx.pollTimer);
        if (ctx.preferenceTimer) {
            clearTimeout(ctx.preferenceTimer);
            ctx.saveRememberedPreferences();
        }
        if (ctx.releaseNotesTimer)
            clearTimeout(ctx.releaseNotesTimer);
        ctx.releaseNotesRequest += 1;
    });
}
