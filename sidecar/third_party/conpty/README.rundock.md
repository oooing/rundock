# RunDock ConPTY compatibility patch

Source: github.com/UserExistsError/conpty v0.1.4, under the included MIT LICENSE.
The Go module replacement uses this local copy; builds require no patched module cache.

Only the following behavior is added:

- `ConPtySuspended()` creates the entry process with `CREATE_SUSPENDED`.
- `Resume()` resumes its primary thread after RunDock attaches its dedicated Windows Job.
- Pipe I/O uses `os.File` to coordinate concurrent reads/writes and close, avoiding
  raw-handle reuse during background group cleanup.

Without suspended creation, a fast script can create detached descendants or exit before
the launcher assigns the Job. Ordinary upstream callers retain the original behavior.
Job ownership, monitoring and cleanup remain in RunDock's `internal/proc` package.

On upstream upgrades, reapply/review these additions and run the Windows background
lifecycle and ConPTY tests, including owner-crash cleanup. Do not drop the license.
