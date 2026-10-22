import { rememberReleaseSession } from '@/utils/releaseSession';
import { nextTick, onBeforeUnmount, onMounted, watch } from 'vue';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installLifecycle(ctx: ReleaseContext) {
    watch([ctx.releaseIntent, ctx.gitOnly, ctx.createTag], () => {
        ctx.gitOnly.value = ctx.releaseIntent.value === 'save-progress';
        ctx.createTag.value = ctx.releaseIntent.value === 'formal';
    }, { flush: 'sync' });
    watch([ctx.buildMode, ctx.createTag, ctx.versionMode, ctx.syncPolicy, ctx.releaseIntent, ctx.localVersionMode, ctx.checksEnabled,
        () => JSON.stringify(Object.entries(ctx.targetChoices.value).map(([id, choice]) => [id, choice.selected]))], ctx.rememberPreferences);
    watch(ctx.checksEnabled, () => { if (ctx.profileReady.value) ctx.editedReleaseOptions.add('checks'); }, { flush: 'sync' });
    watch(ctx.gitOnly, ctx.applySyncPolicy);
    watch(ctx.pushRemote, () => {
        if (ctx.errorCode.value.startsWith('remote_') || ctx.errorCode.value === 'fetch_failed') {
            ctx.error.value = '';
            ctx.errorCode.value = '';
        }
    });
    watch([() => ctx.activeRun.value?.id, () => ctx.activeRun.value?.status], async () => {
        if (ctx.activeRun.value)
            ctx.cancelReleaseNotesDraft();
        await nextTick();
        ctx.bodyRef.value?.scrollTo({ top: 0 });
    });
    watch(ctx.releaseNotesOptionsSignature, (signature) => {
        ctx.cancelReleaseNotesDraft();
        ctx.releaseNotesError.value = '';
        if (!ctx.createTag.value)
            return;
        if (ctx.releaseNotesGeneratedFor.value && signature === ctx.releaseNotesGeneratedFor.value)
            ctx.releaseNotesStale.value = false;
        else {
            ctx.releaseNotesStale.value = !!ctx.releaseNotesGeneratedFor.value;
            if (!ctx.releaseNotesDirty.value)
                ctx.scheduleReleaseNotesDraft();
        }
    }, { flush: 'sync' });
    watch(ctx.plannedTagNames, (tags) => {
        if (ctx.profileReady.value && ctx.createTag.value && tags.length) {
            if (ctx.versionMode.value === 'auto')
                ctx.syncVersionInputs();
            // Manual versions also change generated metadata. Synchronize before
            // submit captures its plan; user-written messages remain untouched.
            ctx.setDefaultCommitMessage();
        }
    }, { flush: 'sync' });
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
        ctx.cancelReleaseNotesDraft();
    });
}
