import { getBaseURL } from '@/api/base'

type ReleaseSession = { appId: string; runId?: string; submittedAt?: number }

const key = () => `launcher.release-session:${getBaseURL()}`

// Session-only navigation state, never credentials, source files or release notes.
export function readReleaseSession(): ReleaseSession | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(key()) || 'null')
    return value && typeof value.appId === 'string' ? value : null
  } catch { return null }
}

export function rememberReleaseSession(value: ReleaseSession | null) {
  try {
    if (value) sessionStorage.setItem(key(), JSON.stringify(value))
    else sessionStorage.removeItem(key())
  } catch { /* Storage restrictions must not prevent publishing. */ }
}
