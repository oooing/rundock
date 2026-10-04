import { api } from '@/api/http';
import { tr } from '@/i18n';
import type { ReleaseCandidate, ReleaseCandidateRequest, ReleaseCheckProfile, ReleaseFileClassification, ReleaseFileRule } from '@/types';
import { hasReleaseIssues } from '@/utils/releaseIssues';
import { recommendedReleaseSelection } from '@/utils/releaseSafety';
import { computed, nextTick } from 'vue';
import type { ExecutionPhase, ReleaseContext } from './context';
// All values belong to this dialog's view model; external data enters through api.
export function installCandidate(ctx: ReleaseContext) {
    ctx.phaseOptions = computed<Array<{
        key: ExecutionPhase;
        label: string;
        hint: string;
        risky: boolean;
    }>>(() => ([
        { key: 'build', label: tr("构建"), hint: tr("生成可运行代码"), risky: false },
        { key: 'package', label: tr("打包"), hint: tr("生成安装包或压缩包"), risky: false },
        { key: 'publish', label: tr("上传"), hint: tr("上传到发布平台"), risky: true },
        { key: 'deploy', label: tr("部署上线"), hint: tr("让线上用户看到新版"), risky: true },
    ]));
    ctx.safetyFiles = computed(() => ctx.candidate.value && !ctx.candidateStale.value ? ctx.candidate.value.classifications : ctx.preflight.value?.classifications || []);
    ctx.candidateRequest = computed<ReleaseCandidateRequest>(() => ({
        skipChecks: !ctx.checksEnabled.value,
        intent: ctx.releaseIntent.value, statusFingerprint: ctx.preflight.value?.statusFingerprint || '', selectedPaths: ctx.selectedPaths.value,
        manualDecisions: ctx.manualDecisions.value, sensitiveExceptions: ctx.sensitiveExceptions.value, targetVersion: ctx.createTag.value ? ctx.primaryTargetVersion.value : '',
        versions: ctx.createTag.value ? ctx.plannedVersions.value.map(version => ({ versionGroupId: version.versionGroupId, targetVersion: version.targetVersion })) : [],
        createTag: ctx.createTag.value, pushRemote: ctx.pushRemote.value, versionMode: ctx.versionMode.value,
        buildMode: ctx.gitOnly.value ? 'none' : ctx.buildMode.value, selectedTargets: ctx.selectedTargets.value,
    }));
    ctx.currentCandidateSignature = computed(() => JSON.stringify(ctx.candidateRequest.value));
    ctx.submissionPlanSignature = computed(() => {
        const { statusFingerprint: _statusFingerprint, ...plan } = ctx.candidateRequest.value;
        return JSON.stringify({ plan, commitMessage: ctx.commitMessage.value, releaseNotes: ctx.createTag.value ? ctx.releaseNotes.value : '' });
    });
    ctx.candidateStale = computed(() => !!ctx.candidate.value && ctx.candidateSignature.value !== ctx.currentCandidateSignature.value);
    ctx.reviewStale = computed(() => !!ctx.candidate.value && (['stale', 'cancelled'].includes(ctx.candidate.value.status) ||
        (ctx.candidateStale.value && ctx.reviewSignature.value !== ctx.currentCandidateSignature.value)));
    ctx.pendingFindings = computed(() => ctx.candidate.value?.sensitiveFindings.filter(finding => !ctx.findingDecisions.value[finding.fingerprint]) || []);
    ctx.candidateReady = computed(() => !ctx.checkingCandidate.value && !ctx.candidateStale.value && !!ctx.candidate.value && (ctx.releaseIntent.value === 'formal' ? ctx.candidate.value.accepted : ctx.candidate.value.canSaveProgress && ctx.candidate.value.status === 'ready'));
    ctx.candidateNeedsAttention = computed(() => ctx.checksEnabled.value && hasReleaseIssues(ctx.candidate.value));
    ctx.showReleaseIssues = async function () {
        await ctx.switchReleaseTab('publish');
        await nextTick();
        const panel = ctx.bodyRef.value?.querySelector<HTMLElement>('.candidate-result');
        panel?.scrollIntoView({ block: 'center' });
        panel?.focus({ preventScroll: true });
    };
    ctx.chooseSafetyFile = function (file: ReleaseFileClassification, included: boolean) {
        if (file.category === 'sensitive' && included)
            return;
        const reviewedFindings = !included && !ctx.reviewStale.value && !ctx.checkingCandidate.value
            ? ctx.candidate.value?.sensitiveFindings.filter(finding => finding.path === file.path) || [] : [];
        ctx.selected.value = { ...ctx.selected.value, [file.path]: included };
        ctx.manualDecisions.value = [...ctx.manualDecisions.value.filter(item => item.path !== file.path), { path: file.path, decision: included ? 'include' : 'exclude', contentFingerprint: file.contentFingerprint }];
        if (reviewedFindings.length) {
            for (const finding of reviewedFindings)
                ctx.findingDecisions.value[finding.fingerprint] = 'exclude';
            ctx.reviewSignature.value = ctx.currentCandidateSignature.value;
            ctx.continueResolvedReview();
        }
    };
    ctx.adoptRecommended = function () { ctx.selected.value = recommendedReleaseSelection(ctx.safetyFiles.value); ctx.manualDecisions.value = []; };
    ctx.recordSensitiveException = function (finding: ReleaseCandidate['sensitiveFindings'][number], reason: string) {
        if (ctx.checkingCandidate.value || ctx.reviewStale.value || !reason.trim() || ctx.findingDecisions.value[finding.fingerprint] ||
            !ctx.candidate.value?.sensitiveFindings.some(item => item.fingerprint === finding.fingerprint && item.contentFingerprint === finding.contentFingerprint))
            return;
        ctx.sensitiveExceptions.value = [...ctx.sensitiveExceptions.value.filter(item => item.findingFingerprint !== finding.fingerprint), { path: finding.path, findingFingerprint: finding.fingerprint, contentFingerprint: finding.contentFingerprint, reason: reason.trim() }];
        ctx.findingDecisions.value[finding.fingerprint] = 'allow';
        ctx.reviewSignature.value = ctx.currentCandidateSignature.value;
        ctx.continueResolvedReview();
    };
    ctx.continueResolvedReview = function () {
        if (ctx.pendingFindings.value.length || ctx.candidate.value?.dependencyFindings.some(item => item.blocked))
            return;
        // Do not publish here. Validate once after the entire batch is resolved.
        ctx.resolvingReview.value = true;
        void ctx.inspectCandidate().finally(() => { ctx.resolvingReview.value = false; });
    };
    ctx.changeReleaseIntent = function (intent: 'formal' | 'save-progress') {
        if (ctx.checkingCandidate.value)
            return;
        const enteringRelease = intent === 'formal' && ctx.releaseIntent.value !== intent;
        ctx.editedReleaseOptions.add('targets');
        ctx.editedReleaseOptions.add('tag');
        ctx.editedReleaseOptions.add('version');
        ctx.releaseIntent.value = intent;
        ctx.gitOnly.value = intent === 'save-progress';
        if (intent === 'save-progress') {
            ctx.editedReleaseOptions.add('targets');
            ctx.editedReleaseOptions.add('push');
            ctx.editedReleaseOptions.add('tag');
            ctx.editedReleaseOptions.add('version');
            ctx.gitOnly.value = true;
            ctx.createTag.value = false;
            ctx.pushRemote.value = false;
            for (const choice of Object.values(ctx.targetChoices.value))
                choice.selected = false;
        }
        else {
            ctx.versionMode.value = 'auto';
            ctx.createTag.value = true;
        }
        if (enteringRelease)
            ctx.selectSingleBuildPlatform(true);
        ctx.setDefaultCommitMessage();
    };
    ctx.refreshSafety = async function () {
        try {
            ctx.applyPreflight(await api.releasePreflight(ctx.props.app.id, false), false, true);
        }
        catch (reason) {
            ctx.error.value = ctx.messageOf(reason);
        }
    };
    ctx.inspectCandidate = async function () {
        if (ctx.checkingCandidate.value || !ctx.preflight.value)
            return false;
        ctx.checkingCandidate.value = true;
        ctx.error.value = '';
        const epoch = ++ctx.candidateEpoch;
        const controller = new AbortController();
        ctx.candidateAbort = controller;
        try {
            await api.saveReleaseProfile(ctx.props.app.id, ctx.profileBody());
            if (ctx.disposed || epoch !== ctx.candidateEpoch)
                return false;
            const fresh = await api.releasePreflight(ctx.props.app.id, false);
            if (ctx.disposed || epoch !== ctx.candidateEpoch)
                return false;
            ctx.applyPreflight(fresh, false, true);
            const request = JSON.parse(JSON.stringify(ctx.candidateRequest.value)) as ReleaseCandidateRequest;
            const signature = JSON.stringify(request);
            const prepared = await api.prepareReleaseCandidate(ctx.props.app.id, request, controller.signal);
            if (ctx.disposed || epoch !== ctx.candidateEpoch)
                return false;
            ctx.candidate.value = prepared;
            ctx.candidateSignature.value = signature;
            ctx.reviewSignature.value = '';
            ctx.findingDecisions.value = {};
            if (request.intent === 'save-progress')
                return prepared.canSaveProgress && prepared.status === 'ready';
            if (request.skipChecks)
                return prepared.checksSkipped === true && prepared.accepted && prepared.status === 'skipped';
            if (prepared.sensitiveFindings.length || prepared.dependencyFindings.some(item => item.blocked))
                return false;
            ctx.candidate.value = { ...prepared, status: 'running' };
            const poll = async () => {
                try {
                    const view = await api.getReleaseCandidate(ctx.props.app.id, prepared.id);
                    if (ctx.checkingCandidate.value && epoch === ctx.candidateEpoch && !ctx.disposed)
                        ctx.candidate.value = view;
                }
                catch { }
                finally {
                    if (ctx.checkingCandidate.value && epoch === ctx.candidateEpoch && !ctx.disposed)
                        ctx.candidatePoll = setTimeout(poll, 800);
                }
            };
            ctx.candidatePoll = setTimeout(poll, 800);
            const checked = await api.checkReleaseCandidate(ctx.props.app.id, prepared.id);
            if (ctx.disposed || epoch !== ctx.candidateEpoch)
                return false;
            ctx.candidate.value = checked;
            return checked.accepted;
        }
        catch (reason) {
            if (!ctx.disposed && epoch === ctx.candidateEpoch) {
                ctx.error.value = ctx.messageOf(reason);
                if (ctx.candidate.value)
                    ctx.candidate.value = { ...ctx.candidate.value, status: 'stale', accepted: false, canSaveProgress: false };
            }
            ;
            return false;
        }
        finally {
            if (epoch === ctx.candidateEpoch) {
                ctx.candidateAbort = null;
                ctx.checkingCandidate.value = false;
                if (ctx.candidatePoll)
                    clearTimeout(ctx.candidatePoll);
            }
        }
    };
    ctx.cancelCandidate = async function () {
        const epoch = ++ctx.candidateEpoch;
        ctx.candidateAbort?.abort();
        ctx.candidateAbort = null;
        if (ctx.candidatePoll)
            clearTimeout(ctx.candidatePoll);
        if (ctx.candidate.value) {
            try {
                const view = await api.cancelReleaseCandidate(ctx.props.app.id, ctx.candidate.value.id);
                if (!ctx.disposed && epoch === ctx.candidateEpoch)
                    ctx.candidate.value = view;
            }
            catch (reason) {
                if (!ctx.disposed && epoch === ctx.candidateEpoch)
                    ctx.error.value = ctx.messageOf(reason);
            }
        }
        if (!ctx.disposed && epoch === ctx.candidateEpoch)
            ctx.checkingCandidate.value = false;
    };
    ctx.saveSafetyConfig = async function (rules: ReleaseFileRule[], checks: ReleaseCheckProfile[]) {
        if (!ctx.releaseConfig.value)
            return;
        ctx.configSaving.value = true;
        try {
            const saved = await api.saveReleaseConfig(ctx.props.app.id, { ...ctx.releaseConfig.value, fileRules: rules, checkProfiles: checks });
            ctx.applyReleaseConfig(saved);
            await ctx.refreshSafety();
        }
        catch (reason) {
            ctx.error.value = ctx.messageOf(reason);
        }
        finally {
            ctx.configSaving.value = false;
        }
    };
}
