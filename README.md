# woma

**woma** (*wo*rktree *ma*nager) is a terminal tool for living with
[git worktrees](https://git-scm.com/docs/git-worktree). It finds every
worktree across your repositories, tells you which ones are done with, cleans
them up, creates new ones the way you like, and jumps between them.

```
woma  ~/code
4 worktrees in 2 repositories · 38.2 MB

 api  3 worktrees · 38.2 MB
  ▸ [ ] .worktrees/feature-login  feature-login    2h    38.2 MB  modified 1 unpushed
    [x] .worktrees/fix-typo       fix-typo         3w     8.0 KB  merged
    [x] .worktrees/old-spike      old-spike        4mo    8.0 KB  gone stale
 web  1 worktree · 4.0 KB
    [ ] web-redesign              redesign         1d     4.0 KB  locked no commits
──────────────────────────────────────────────────────────────────────────────
 path    ~/code/api/.worktrees/feature-login
 branch  feature-login  no upstream
 state   1 commit unpushed · 1 changed file
 commit  add login form · 2h ago
 active  2h ago · 38.2 MB
 changes ?? data.bin

 2 selected · 16.0 KB
↑/↓ move · space select · m select safe · / search · s sort (name) · enter delete · ? help · q quit
```

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [The browser (TUI)](#the-browser-tui)
- [Labels: what's safe to delete?](#labels-whats-safe-to-delete)
- [Commands](#commands)
- [Shell integration: `wcd` and `wnew`](#shell-integration-wcd-and-wnew)
- [Configuration](#configuration)
- [What woma will and won't delete](#what-woma-will-and-wont-delete)
- [Development](#development)

## Install

Requires [Go](https://go.dev/dl/) 1.24+ and `git` on your `PATH`.

```sh
git clone <this-repo> woma
cd woma
go install .                  # puts `woma` in $(go env GOPATH)/bin
```

Or build it in place and link it onto your `PATH`:

```sh
go build -o woma .
ln -s "$PWD/woma" ~/.local/bin/woma
```

## Quick start

```sh
woma ~/code                   # browse every worktree under ~/code
woma clean ~/code --safe      # delete the ones that are merged and clean (asks first)
woma new feat/login-page      # create a worktree for a branch, inside a repo
eval "$(woma shell-init)"     # then `wcd` to jump to a worktree
```

## The browser (TUI)

```sh
woma                          # scan the current directory (or configured roots)
woma ~/code ~/work            # scan specific folders
```

woma shows worktrees as fast as it can find them:

1. If you start it inside a repository (or any folder of one), that
   repository's worktrees appear immediately, pinned to the top, since git
   already knows them.
2. Meanwhile it scans the folders below, nearest first and in parallel, and
   adds each repository to the list as soon as it is found.
3. Each worktree's git status (labels) and its disk size are worked out
   separately, so labels don't wait for slow size calculations. Values still
   being worked out show as `…`; the header shows the progress.

The cursor stays on the first row while the list fills in, until you move
it. Deleting a worktree waits until its git status is known.

Worktrees are grouped by repository. Each row shows the folder, branch, time
since last activity (newest commit or file change), disk size and
[labels](#labels-whats-safe-to-delete). The **detail pane** under the list
shows the full path, upstream and ahead/behind counts, the last commit and
the changed files of the worktree under the cursor.

### Keys

| Key               | Action                                                                  |
| ----------------- | ----------------------------------------------------------------------- |
| `↑` `↓` / `k` `j` | Move                                                                    |
| `pgup` `pgdown`   | Move half a page                                                        |
| `g` / `G`         | First / last worktree                                                   |
| `space` / `x`     | Select or unselect                                                      |
| `a`               | Select all in the current repository                                    |
| `A`               | Select all shown                                                        |
| `m`               | **Select safe ones**: merged or gone, nothing to lose, not locked       |
| `/`               | Search by path, branch or repository (`enter` keeps it, `esc` clears)   |
| `s`               | Sort by name → size (largest first) → activity (oldest first)           |
| `i`               | Show or hide the detail pane                                            |
| `enter` / `d`     | Delete selected worktrees (or the one under the cursor)                 |
| `o`               | Open in your editor                                                     |
| `t`               | Open in a new tmux window (when running inside tmux)                    |
| `c`               | Clean build artifacts (see below)                                       |
| `L`               | Lock or unlock (`git worktree lock`)                                    |
| `M`               | Move to a new folder (`git worktree move`; the path must not exist yet) |
| `R`               | Repair the repository's worktree links (`git worktree repair`)          |
| `r`               | Rescan                                                                  |
| `?`               | Help                                                                    |
| `q`               | Quit                                                                    |

### Deleting

Select worktrees and press `enter`. Deleting waits until the selected
worktrees have been inspected, so woma knows what you'd lose. The
confirmation screen lists what will be removed and warns about anything that
would be lost: uncommitted changes, or commits on a detached HEAD that no
branch contains. It also flags locked worktrees.

Press `b` on that screen to **also delete the branches**. Only branches that
are merged into the default branch or fully pushed are deleted; branches with
unpushed commits are kept and marked as such. Press `y` to go ahead.

While deleting, woma shows an overall progress bar (space freed so far) and a
bar per worktree; up to three are removed in parallel. `ctrl+c` is disabled
until it finishes. When you quit, it prints how many worktrees it removed and
how much space it freed. Freed space counts only files whose last link is
removed: files hard-linked from a package manager's store (pnpm) don't count.

### Cleaning build artifacts

`c` removes git-ignored files (`node_modules`, build output, caches) from the
worktree under the cursor without deleting the worktree, the same as
`git clean -fdX`. It shows what would be removed and asks first. Tracked
files, untracked files you created, and files matching the `copy` patterns
(by default `.env` and `.env.*`) are always kept.

### Opening worktrees

`o` opens the worktree with the `editor` from the config, falling back to
`$VISUAL`, `$EDITOR`, then `open` (macOS) or `xdg-open` (Linux). Terminal
editors take over the screen and return to woma when you exit them.

`t` opens a new tmux window in the worktree, named after its folder.

## Labels: what's safe to delete?

| Label        | Meaning                                                                                   |
| ------------ | ----------------------------------------------------------------------------------------- |
| `merged`     | The branch's commits are all in the default branch (e.g. `main` or `origin/main`).         |
| `gone`       | The branch's remote branch was deleted, which usually means its pull request was merged.  |
| `no commits` | The branch was created but never committed to.                                             |
| `stale`      | No commits or file changes for longer than `stale_after` (default 30 days).               |
| `modified`   | Uncommitted or untracked changes. **Deleting the worktree loses them.**                  |
| `N unpushed` | Commits that exist only locally. On a branch they survive worktree deletion (branches are kept); on a detached HEAD they would be lost. |
| `locked`     | Locked with `git worktree lock`.                                                          |
| `missing`    | git still tracks the worktree but its folder is gone; deleting cleans up git's records.   |
| `unknown`    | A git check failed, so woma can't tell what deleting would lose. Treated as unsafe.      |

A worktree is **safe** (what `m` and `--safe` select) when it is `merged`,
`gone` or `missing`, deleting it loses nothing, and it is not locked.
`no commits` worktrees are not considered safe: you probably just created
them.

Squash-merged branches show as `gone` with unpushed commits, because their
commits never reached `main` as such. They are still safe to remove as
worktrees, but their branches are kept unless you delete them yourself.

## Commands

All commands accept flags before or after paths. Paths default to the
configured `roots`, then the current directory.

### `woma [path...]`

Opens the browser described above.

### `woma pick [path...]`

A focused picker for jumping to a worktree. It shows the same list as the
browser, but only to choose from: move, search (`/`), sort (`s`) and toggle
details (`i`), then press `enter`. woma prints the path of the worktree under
the cursor and exits. Nothing else goes to stdout, so it works in `$(...)`.

```sh
cd "$(woma pick ~/code)"
```

There are no checkboxes, and everything that changes worktrees (delete,
clean, lock, move, repair, open) is disabled, so a stray key can't do
damage. Quitting with `q` prints nothing and exits with an error, so
`cd "$(woma pick)"` leaves you where you were. Worktrees whose folder is
missing can't be picked.

`--multi` lets you select several worktrees with `space` (plus `a`, `A` and
`m`). `enter` then prints each selected path on its own line, or the one
under the cursor if none are selected. It's for scripts:

```sh
woma pick --multi | xargs -I{} git -C {} pull   # update several worktrees
code $(woma pick --multi)                       # open a few in your editor
```

### `woma new <branch>`

Run inside a repository. Creates a worktree for the branch and prints its
path:

1. The folder comes from `worktree_dir` (default
   `{repo}/.worktrees/{name}`), where `{name}` is the branch with `/` turned
   into `-`: `feat/login-page` → `.worktrees/feat-login-page`. Worktrees
   created inside the repository are added to `.git/info/exclude` so they
   never show up as untracked files. For bare repositories (`repo.git`, or a
   `.bare` folder) `{repo}` is the folder that contains the git directory.
2. If the branch exists locally or on a remote it is checked out; otherwise
   it is created from your current `HEAD` (or `--base`).
3. Files matching `copy` (default `.env`, `.env.*`) are **copied, never
   symlinked** from the main worktree, keeping their relative paths, when the
   new checkout doesn't already have them.
4. Dependencies are installed with the `install` command, or one detected
   from lockfiles: `pnpm install`, `bun install`, `yarn install` or
   `npm install`.

| Flag              | Effect                                                   |
| ----------------- | -------------------------------------------------------- |
| `--base <ref>`    | Create the branch from this ref instead of `HEAD`        |
| `--path <dir>`    | Create the worktree here instead of `worktree_dir` (relative paths are relative to `-C`) |
| `--no-copy`       | Don't copy env files                                     |
| `--no-install`    | Don't install dependencies                               |
| `-C <dir>`        | Run as if started in `<dir>`                             |

```sh
woma new feat/login-page
woma new hotfix/crash --base v2.3.0
cd "$(woma new feat/search --no-install)"
```

### `woma list [path...]`

Prints worktrees as a table (or JSON with `--json`), with the same labels as
the browser. Accepts the filters below.

```sh
woma list ~/code --stale
woma list --json | jq '.[] | select(.safe) | .path'
```

JSON fields: `repo`, `repo_path`, `path`, `branch`, `head`, `upstream`,
`size` (bytes), `last_activity`, `merged`, `gone`, `stale`, `dirty`,
`locked`, `missing`, `ahead`, `behind`, `unpushed`, `safe`.

### `woma clean [path...]`

Deletes the worktrees matching the filters. It prints the plan, then asks
for confirmation (or pass `--yes`; without a terminal it refuses unless
`--yes` is given). Worktrees that are locked or would lose work are skipped
unless you pass `--force`.

```sh
woma clean --safe --dry-run             # preview
woma clean ~/code --merged --older-than 2w
woma clean --gone --delete-branch --yes # for scripts and cron
```

| Flag              | Effect                                                                      |
| ----------------- | --------------------------------------------------------------------------- |
| `--dry-run`       | Show what would be deleted, delete nothing                                  |
| `--yes`           | Don't ask for confirmation                                                  |
| `--force`         | Also delete locked worktrees and ones with uncommitted work                 |
| `--delete-branch` | Also delete branches that are merged or fully pushed                        |
| `--all`           | Match every worktree (needed when no filter is given)                       |

### Filters (`list` and `clean`)

Filters combine: a worktree must match all of them.

| Filter               | Matches worktrees that…                                              |
| -------------------- | -------------------------------------------------------------------- |
| `--merged`           | are `merged`                                                         |
| `--gone`             | are `gone`                                                           |
| `--stale`            | are `stale` (see `stale_after`)                                      |
| `--older-than <age>` | have had no activity for `<age>`: `36h`, `30d`, `2w`, `6mo`, `1y`    |
| `--clean`            | lose nothing when deleted                                            |
| `--safe`             | are [safe](#labels-whats-safe-to-delete)                             |

### `woma shell-init [zsh|bash|fish]`, `woma config`, `woma version`

See the next two sections; `version` prints the version.

## Shell integration: `wcd` and `wnew`

A program can't change your shell's directory, so woma provides two small
shell functions. Add one line to your shell's startup file:

```sh
# ~/.zshrc or ~/.bashrc
eval "$(woma shell-init)"

# ~/.config/fish/config.fish
woma shell-init fish | source
```

Then:

```sh
wcd                 # pick a worktree and cd into it (single choice)
wcd ~/code          # pick among worktrees under ~/code
wnew feat/search    # create a worktree and cd into it
```

## Configuration

Everything is optional. woma reads `~/.config/woma/config.toml` (or
`$XDG_CONFIG_HOME/woma/config.toml`, or the file in `$WOMA_CONFIG`).
`woma config` prints the location and the settings in effect.

```toml
# Folders scanned when no path is given (default: the current directory).
roots = ["~/code", "~/work"]

# Folder names or globs skipped while scanning (node_modules always is).
ignore = ["vendor", "*.cache"]

# How many levels below a root to scan; 0 means no limit.
max_depth = 0

# No activity for this long marks a worktree as stale.
stale_after = "30d"

# Command used by `o` to open a worktree.
editor = "code"

# Where `woma new` creates worktrees. {repo} is the main checkout's path,
# {repo_name} its folder name, {name} the branch with "/" turned into "-".
# Relative paths are relative to the main checkout.
worktree_dir = "{repo}/.worktrees/{name}"
# e.g. siblings of the repository instead:
# worktree_dir = "{repo}/../{repo_name}-{name}"

# Files copied into new worktrees (and kept by artifact cleaning).
copy = [".env", ".env.*"]

# Install command for new worktrees: "" detects it from lockfiles,
# "none" disables it.
install = ""
```

## What woma will and won't delete

- Only **linked worktrees** (created with `git worktree add`) are listed.
  The main checkout of a repository is never shown or deleted, and woma only
  wipes folders that have a worktree's `.git` file.
- Deleting a worktree removes its folder and git's record of it, like
  `git worktree remove --force`. woma first moves the folder aside and has
  git unregister it, and only then deletes the files, so a failure leaves
  the worktree untouched. If deletion is interrupted (e.g. the machine
  sleeps), a hidden `.<name>.woma-deleting-*` folder may be left next to
  where the worktree was; it's no longer a worktree and safe to delete.
- Anything woma couldn't fully inspect is treated as unsafe: it is never
  selected by `m` or `--safe`, `clean` skips it without `--force`, and its
  branch is never deleted.
- **Branches are kept** unless you ask for them to be deleted (`b` in the
  browser, `--delete-branch` in `clean`), and even then only merged or fully
  pushed branches are deleted.
- Worktrees outside the scanned folders are listed too, as long as their
  repository is found (or the other way round).

## Development

```sh
go test ./...        # run the tests
go run . ~/code      # run without installing
go build -o woma .   # build the binary
```

The tests never touch your repositories or settings: each one builds
throwaway repositories (and remotes) in a temporary directory, and git runs
with your global config disabled.

```
main.go, cmd_*.go     command-line entry points
internal/core         inspection, labels, filters, safety rules, create/remove
internal/git          scanning and git commands
internal/fsutil       disk usage and deletion with progress
internal/config       config file
internal/tui          the terminal UI (Bubble Tea)
internal/testutil     helpers that build test repositories
```
