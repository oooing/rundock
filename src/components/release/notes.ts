import { api } from '@/api/http';
import type { ReleasePreflight } from '@/types';
import type { ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installNotes(ctx: ReleaseContext) {
    ctx.scheduleReleaseNotesDraft = function (delay = 80) {
        if (ctx.targetSelectionMissing.value)
            return;
        if (!ctx.createTag.value || !ctx.preflight.value || ctx.releaseNotesDirty.value || ctx.activeRun.value || ctx.disposed)
            return;
        if (ctx.releaseNotesTimer)
            clearTimeout(ctx.releaseNotesTimer);
        ctx.releaseNotesTimer = setTimeout(() => { ctx.releaseNotesTimer = null; void ctx.generateReleaseNotesDraft(); }, delay);
    };
    ctx.generateReleaseNotesDraft = async function (force = false, overwriteConfirmed = false) {
        if (ctx.targetSelectionMissing.value)
            return;
        const pf = ctx.preflight.value;
        if (!pf || !ctx.createTag.value || ctx.releaseNotesLoading.value)
            return;
        // A scheduled draft may start after the user has begun typing.
        if (!force && ctx.releaseNotesDirty.value)
            return;
        if (force && ctx.releaseNotesDirty.value && !overwriteConfirmed) {
            ctx.confirmAction.value = 'regenerate-notes';
            return;
        }
        const sourceSignature = ctx.releaseNotesOptionsSignature.value;
        const requestId = ++ctx.releaseNotesRequest;
        ctx.releaseNotesLoading.value = true;
        ctx.releaseNotesError.value = '';
        try {
            const draft = await api.createReleaseNotesDraft(ctx.props.app.id, {
                statusFingerprint: pf.statusFingerprint,
                selectedPaths: ctx.selectedPaths.value,
                selectedTargets: ctx.selectedTargets.value,
            });
            if (ctx.disposed || requestId !== ctx.releaseNotesRequest)
                return;
            if (sourceSignature !== ctx.releaseNotesOptionsSignature.value) {
                ctx.releaseNotesStale.value = true;
                return;
            }
            ctx.releaseNotes.value = draft.text;
            ctx.releaseNotesBaseTag.value = draft.baseTag;
            ctx.releaseNotesSourceFingerprint.value = draft.sourceFingerprint;
            ctx.releaseNotesGeneratedFor.value = sourceSignature;
            ctx.releaseNotesDirty.value = false;
            ctx.releaseNotesStale.value = false;
        }
        catch (reason) {
            if (requestId === ctx.releaseNotesRequest)
                ctx.releaseNotesError.value = ctx.messageOf(reason);
        }
        finally {
            if (requestId === ctx.releaseNotesRequest) {
                ctx.releaseNotesLoading.value = false;
                if (sourceSignature !== ctx.releaseNotesOptionsSignature.value)
                    ctx.scheduleReleaseNotesDraft(120);
            }
        }
    };
    ctx.onReleaseNotesInput = function () {
        if (ctx.releaseNotesTimer) {
            clearTimeout(ctx.releaseNotesTimer);
            ctx.releaseNotesTimer = null;
        }
        ctx.releaseNotesRequest += 1;
        ctx.releaseNotesLoading.value = false;
        if (!ctx.releaseNotesGeneratedFor.value)
            ctx.releaseNotesGeneratedFor.value = ctx.releaseNotesOptionsSignature.value;
        ctx.releaseNotesDirty.value = true;
        ctx.releaseNotesError.value = '';
    };
    ctx.applyPreflight = function (raw: ReleasePreflight, initial = false, resetFiles = true) {
        const pf = ctx.normalizePreflight(raw);
        ctx.preflight.value = pf;
        ctx.remoteName.value = pf.profile?.remoteName || ctx.remoteName.value || 'origin';
        ctx.versionStrategy.value = pf.profile?.versionStrategy || ctx.versionStrategy.value || 'auto';
        ctx.preReleaseCommand.value = pf.profile?.preReleaseCommand || '';
        if (initial) {
            const remembered = ctx.readLocalPreferences();
            if (!ctx.editedReleaseOptions.has('intent') && remembered.releaseIntent)
                ctx.releaseIntent.value = remembered.releaseIntent;
            if (!ctx.editedReleaseOptions.has('local-version') && remembered.localVersionMode)
                ctx.localVersionMode.value = remembered.localVersionMode;
            if (!ctx.editedReleaseOptions.has('checks') && remembered.checksEnabled !== undefined)
                ctx.checksEnabled.value = remembered.checksEnabled;
            if (!ctx.editedReleaseOptions.has('push')) {
                ctx.syncPolicy.value = pf.profile?.syncPolicy || remembered.syncPolicy || 'auto';
                ctx.localSyncPolicy.value = remembered.localSyncPolicy || ctx.syncPolicy.value;
            }
            const keepTargets = ctx.editedReleaseOptions.has('targets') || ctx.editedReleaseOptions.has('build');
            if (!keepTargets) {
                ctx.buildMode.value = pf.profile?.buildMode || remembered.buildMode || 'github';
                for (const target of ctx.configuredTargets.value)
                    ctx.targetChoices.value[target.id] = ctx.defaultTargetChoice(target);
                if (!ctx.restoreTargetPreferences()) ctx.selectSingleBuildPlatform();
            }
            if (!ctx.editedReleaseOptions.has('tag'))
                ctx.createTag.value = ctx.releaseIntent.value === 'formal';
            if (!ctx.editedReleaseOptions.has('version'))
                ctx.versionMode.value = remembered.versionMode || (pf.profile?.versionMode === 'manual' || pf.profile?.versionMode === 'auto' ? pf.profile.versionMode : 'auto');
        }
        ctx.applySyncPolicy();
        if (ctx.createTag.value)
            ctx.syncVersionInputs();
        ctx.setDefaultCommitMessage();
        if (resetFiles)
            ctx.resetSelection(pf);
        ctx.preflightStale.value = false;
        ctx.scheduleReleaseNotesDraft();
    };
}
