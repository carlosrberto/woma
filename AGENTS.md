# AGENTS.md

This file provides project-wide guidance for AI coding agents (Claude Code, Codex, Cursor, etc.) working with code in this repository. Claude Code reads it as `CLAUDE.md` via the symlink at the repo root.

woma is a git worktree manager in Go: a TUI (Bubble Tea) that finds the
repositories and worktrees under a folder, shows their size and status,
and deletes, creates, moves, locks and cleans them; plus non-interactive
commands (`list`, `clean`, `pick`, `new`, ...). The README is the user
reference: every command, flag, key, label, config setting and JSON field
is documented there.

## Commands

```sh
make build                 # ./woma (~/.local/bin/woma links to it)
make check                 # gofmt, go vet, staticcheck, shellcheck, race tests
go test ./internal/git -run TestInspectFreshBranchFromRemoteBase   # one test
go run . ~/code            # the TUI without installing
make                       # every target, including the release ones
```

Run `make check` to completion before every commit and commit only when it
passes; redirect its output to a file rather than piping it (`make check >
check.log 2>&1`). `make setup` installs the pre-push hook (it runs
`make test`).

Needs Go 1.24+, git, shellcheck, staticcheck.

## Rules

- **Never touch the user's real repositories.** Tests build throwaway
  repos (and remotes) in temp dirs through `internal/testutil`, with git's
  global and system config disabled (`testutil.IsolateGit`) and
  `WOMA_CONFIG` pointing at a temp file. Against a real repo, only
  read-only commands (`woma list`, `git log`, `git reflog`...): never
  delete, clean, move, lock or create there. To try the TUI, run it on a
  throwaway tmux server (`tmux -L womatest ...`), never the default one,
  and send keys one at a time (Bubble Tea merges fast keystrokes).
- **Examples use generic, made-up names - always.** In tests, fixtures,
  docs, the README, comments and commit messages: repos like `acme-api` /
  `web-app`, branches like `feat/search`, issue keys like `PRJ-123`. Never
  copy a real repo, branch, issue key, employer or path from the user's
  machine or screenshots - not even "just for a test". Before committing,
  check the diff for anything that came from real data.
- **Commit freely, push only when asked.** Commit finished, checked work
  without asking; always ask before `git push`, tags, releases (`make
  release`), or changes to the GitHub repo or the tap.
- **Deleting stays unsafe by default.** Whatever woma can't verify counts
  as losing work: an inspection that failed or is still running
  (`Status.Complete` false) blocks `Safe`, `clean` and branch deletion.
  Keep new checks on that side.
- **Docs change with the code**, in the same commit: the README (commands,
  flags, keys, labels, config, `--json` fields), the usage text in
  `main.go`, `docs/releasing.md` for the release flow. Describe what woma
  does now; history belongs in git.

## Architecture

`main.go` dispatches subcommands to `cmd_*.go`. `help`, `version`,
`shell-init` and `config` run before the config file is loaded, so a
broken config never breaks them. `pick` draws the TUI on stderr: stdout
carries only the picked path(s), for `wcd`.

Packages, bottom up (`imports_test.go` enforces it):

- `internal/git` - discovery and git commands. `DiscoverEach` reports the
  repo containing the root first (`rev-parse --git-common-dir`), then walks
  breadth first with parallel walkers (`walk.go`) and emits each repo as it
  loads. `Inspect` (`inspect.go`) fills `Status`: dirty, upstream
  gone/ahead/behind, merged, unpushed, last commit.
- `internal/fsutil` - disk usage (hard-link aware: a file counts only when
  all its links are inside the tree) and removal with progress.
- `internal/config` - the TOML config; unknown keys are errors.
- `internal/core` - an `Entry` (repo + worktree + size + status) and the
  rules on it: `Labels`, `Safe`, `LosesWork`, `CanDeleteBranch`, filters.
  `Remove` renames the folder aside, lets git unregister the worktree,
  then deletes it, restoring the folder if git fails. `create.go` is
  `woma new` (naming, copying `.env` files, installing dependencies).
- `internal/tui` - the Bubble Tea model. Scanning streams: discovery,
  status checks and size measurements arrive as events on one channel,
  wrapped with a scan generation so a rescan drops stale ones; status and
  size work have separate semaphores. Rows show as soon as they're found,
  sizes fill in later; the cursor follows the first row until the user
  moves it (`cursorMoved`).

"Merged" is subtle: a branch with no commits of its own is an ancestor of
the base too. `neverCommitted` reads the branch's reflog to tell them
apart ("no commits"), ignoring renames and treating creation from another
remote branch (e.g. `origin/main`) as bringing no commits. Reflogs expire
(90 days by default), so an old untouched branch falls back to "merged".

## Tests

- git and core tests run real git on temp repos (`testutil.NewRepo`,
  `NewRepoWithRemote`); no mocks.
- TUI tests drive the model directly with messages and keys (`drive`,
  `key` in `model_test.go`) and assert on its state and `View()`.
- `main_test.go` runs the commands in-process with captured output.

## Style

- Comments and docs are short, plain and in the present tense, matching
  the code around them; doc comments say what a thing is or does.
- Commits are conventional: `type(scope): what, lowercase` (`fix(status):
  ...`, `feat(pick): ...`), with a body when the why isn't obvious; the
  release changelog is grouped from these types. No AI attribution lines
  (co-author trailers, "generated with" notes): the user's hooks reject
  the commit.
- Releases: `make bump-patch|minor|major`, `make prepare`, `make release`
  (tag, GitHub release with notes, Homebrew formula in
  `carlosrberto/homebrew-tap`, built from source - no binaries).
  `docs/releasing.md` has the details.
