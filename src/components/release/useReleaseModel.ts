import type { AppView } from '@/types';
import { installCandidate } from './candidate';
import { installConfiguration } from './configuration';
import type { ReleaseContext } from './context';
import { installLifecycle } from './lifecycle';
import { installNotes } from './notes';
import { installPlatforms } from './platforms';
import { installPreferences } from './preferences';
import { installProgress } from './progress';
import { installSelection } from './selection';
import { installState } from './state';
import { installSubmission } from './submission';
import { installSync } from './sync';
export function useReleaseModel(props: {
    app: AppView;
}, emit: (e: 'close') => void) {
    // Install pure state and lazy derivations before registering watchers or starting I/O.
    const ctx = { props, emit } as ReleaseContext;
    installState(ctx);
    installCandidate(ctx);
    installSelection(ctx);
    installPlatforms(ctx);
    installProgress(ctx);
    installConfiguration(ctx);
    installSync(ctx);
    installPreferences(ctx);
    installNotes(ctx);
    installSubmission(ctx);
    installLifecycle(ctx);
    return ctx;
}
