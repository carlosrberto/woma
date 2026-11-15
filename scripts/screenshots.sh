#!/usr/bin/env bash
# woma's screenshots, for the README: made-up repositories in a temp dir
# (their own HOME, git config and woma config), woma run on a throwaway
# tmux server, each scene captured (tmux capture-pane) and drawn as an SVG
# (scripts/render). Never your repositories or your default tmux server.
#
#   scripts/screenshots.sh   writes docs/screenshots/<scene>.svg
#   WOMA_BIN=path/to/woma    another build (default: ./woma)
#   SCREENSHOTS_DIR=dir      somewhere else
#   KEEP=1                   keep the temp dir, to look at the fixture
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${WOMA_BIN:-$ROOT/woma}"
OUT="${SCREENSHOTS_DIR:-$ROOT/docs/screenshots}"
WORK="$(cd "$(mktemp -d)" && pwd -P)" # resolved: woma shows resolved paths
real_home="$HOME"
mkdir -p "$OUT"

# The clock is frozen (WOMA_NOW) and every commit and file is dated from
# it, so every run draws the same ages, labels and sizes.
NOW=1780315200 # 2026-06-01T12:00:00Z
H=3600 D=86400

V() { tmux -L womatest-shots -f "$WORK/tmux.conf" "$@"; }
# shellcheck disable=SC2317,SC2329 # invoked by the trap
cleanup() {
  V kill-server 2>/dev/null || true
  if [ -n "${KEEP:-}" ]; then echo "kept $WORK" >&2; else rm -rf "$WORK"; fi
}
trap cleanup EXIT
go build -o "$WORK/render" "$ROOT/scripts/render" # before HOME moves: your Go caches

# --- The fixture ---

export HOME="$WORK/home" WOMA_CONFIG="$WORK/config.toml" WOMA_NOW=2026-06-01T12:00:00Z
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME="Dev" GIT_AUTHOR_EMAIL="dev@example.com" GIT_COMMITTER_NAME="Dev" GIT_COMMITTER_EMAIL="dev@example.com"
mkdir -p "$HOME/code" "$WORK/remotes"
printf 'stale_after = "30d"\n' > "$WOMA_CONFIG"
CODE="$HOME/code"

# iso <epoch>: the time as touch -d takes it.
iso() { date -u -r "$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ; }
# at <seconds ago> <command...>: a git command dated that long before NOW.
at() {
  local t=$((NOW - $1))
  shift
  GIT_AUTHOR_DATE="@$t +0000" GIT_COMMITTER_DATE="@$t +0000" "$@"
}
# commit <dir> <seconds ago> <file> <message>: a change to file, committed.
commit() {
  echo "$4" >> "$1/$3"
  git -C "$1" add "$3"
  at "$2" git -C "$1" commit -q -m "$4"
}
# stamp <dir> <seconds ago>: every file in the worktree last changed then.
stamp() {
  find "$1" -name .git -prune -o -exec touch -h -d "$(iso $((NOW - $2)))" {} +
}
# blob <file> <KiB>: a file of that size.
blob() { mkdir -p "$(dirname "$1")" && head -c $(($2 * 1024)) /dev/zero > "$1"; }

# acme-api: a remote, a branch in progress, a merged one, one whose remote
# branch is gone, a fresh locked one.
api="$CODE/acme-api"
git init -q --bare -b main "$WORK/remotes/acme-api.git"
git init -q -b main "$api"
git -C "$api" remote add origin "$WORK/remotes/acme-api.git"
commit "$api" $((180 * D)) README.md "initial commit"
commit "$api" $((35 * D)) server.go "add a health check"
git -C "$api" push -q -u origin main

at $((5 * D)) git -C "$api" worktree add -q -b feat/login "$api/.worktrees/feat-login"
blob "$api/.worktrees/feat-login/assets/login.png" 2048
git -C "$api/.worktrees/feat-login" add assets
commit "$api/.worktrees/feat-login" $((1 * D)) login.go "add the login form"
git -C "$api/.worktrees/feat-login" push -q -u origin feat/login
commit "$api/.worktrees/feat-login" $((2 * H)) login.go "validate the login form"
echo "draft" > "$api/.worktrees/feat-login/notes.md"
stamp "$api/.worktrees/feat-login" $((2 * H))

git -C "$api" worktree add -q -b fix/typo "$api/.worktrees/fix-typo"
commit "$api/.worktrees/fix-typo" $((21 * D)) README.md "fix a typo in the readme"
git -C "$api/.worktrees/fix-typo" push -q -u origin fix/typo
git -C "$api" merge -q --ff-only fix/typo
git -C "$api" push -q origin main
stamp "$api/.worktrees/fix-typo" $((21 * D))

git -C "$api" worktree add -q -b spike/cache "$api/.worktrees/old-spike" main~2
commit "$api/.worktrees/old-spike" $((120 * D)) cache.go "try a cache"
git -C "$api/.worktrees/old-spike" push -q -u origin spike/cache
git -C "$api" push -q origin --delete spike/cache
git -C "$api" fetch -q --prune
stamp "$api/.worktrees/old-spike" $((120 * D))

git -C "$api" worktree add -q -b feat/search "$api/.worktrees/feat-search"
git -C "$api" worktree lock --reason "in review" "$api/.worktrees/feat-search"
stamp "$api/.worktrees/feat-search" $((1 * D))

# web-app: no remote; a redesign next to it with changes, merged docs, and
# a worktree whose folder is gone.
web="$CODE/web-app"
git init -q -b main "$web"
commit "$web" $((90 * D)) index.html "initial commit"
commit "$web" $((60 * D)) app.css "add the styles"

git -C "$web" worktree add -q -b feat/redesign "$CODE/web-app-redesign"
blob "$CODE/web-app-redesign/assets/hero.jpg" 512
commit "$CODE/web-app-redesign" $((3 * D)) app.css "a new header"
echo "/* wip */" >> "$CODE/web-app-redesign/app.css"
stamp "$CODE/web-app-redesign" $((1 * D))

git -C "$web" worktree add -q -b docs/setup "$web/.worktrees/docs-setup"
commit "$web/.worktrees/docs-setup" $((14 * D)) SETUP.md "the setup guide"
git -C "$web" merge -q --ff-only docs/setup
stamp "$web/.worktrees/docs-setup" $((14 * D))

git -C "$web" worktree add -q -b demo/old "$web/.worktrees/old-demo"
rm -rf "$web/.worktrees/old-demo"

# --- The scenes ---

COLS=112 ROWS=24
# The terminal: tokyonight's colours (the renderer's), told to woma's
# lipgloss as a dark background.
printf '%s\n' "set -g window-style 'fg=#c0caf5,bg=#1a1b26'" "set -g default-terminal tmux-256color" \
  "set -g status off" "set -g remain-on-exit on" "set -g escape-time 0" > "$WORK/tmux.conf"

# scene <command...>: a fresh session running it in ~/code, the size of
# the shots.
scene() {
  V kill-session -t s 2>/dev/null || true
  V new-session -d -s s -x "$COLS" -y "$ROWS" -c "$CODE" -- env COLORTERM=truecolor "$@"
}
screen() { V capture-pane -p -t s; }
# wait_screen <text> [has|lacks]: until the screen has it (or lacks it), 30 s at most.
wait_screen() {
  for _ in $(seq 300); do
    if screen | grep -qF -- "$1"; then [ "${2:-has}" = has ] && return 0; else [ "${2:-has}" = lacks ] && return 0; fi
    sleep 0.1
  done
  echo "screenshots: waited for the screen to ${2:-has} \"$1\":" >&2
  screen >&2
  exit 1
}
# settled: until the screen stops changing.
settled() {
  local a b
  a="$(V capture-pane -e -p -t s)"
  for _ in $(seq 50); do
    sleep 0.2
    b="$(V capture-pane -e -p -t s)"
    [ "$a" = "$b" ] && return 0
    a="$b"
  done
}
# loaded: until woma has listed, checked and measured every worktree.
loaded() {
  wait_screen "worktrees in 2 repositories"
  wait_screen "checking git" lacks
  wait_screen "measuring" lacks
  wait_screen "…" lacks
  settled
}
# key <key>: one key, then the screen caught up (Bubble Tea merges fast keys).
key() { V send-keys -t s "$1" && settled; }
# shot <scene> <title>: the screen, as $OUT/<scene>.svg; refused when it
# shows a real path or the fixture's own.
shot() {
  if screen | grep -qF -e "$real_home" -e "$WORK" -e /var/folders/ -e /private/ -e /tmp/; then
    echo "screenshots: $1 shows a real path - not written:" >&2
    screen >&2
    exit 1
  fi
  V capture-pane -e -p -t s > "$WORK/$1.ansi"
  "$WORK/render" -trim -title "$2" "$WORK/$1.ansi" "$OUT/$1.svg"
  echo "  $1"
}

echo "screenshots -> $OUT"

# The browser, the safe ones selected (m).
scene "$BIN" "$CODE"
loaded
key m
shot browser "woma ~/code"

# The delete confirmation, branches too (b).
key Enter
wait_screen "branches"
key b
shot delete "woma ~/code"
key Escape

# The picker.
scene "$BIN" pick "$CODE"
loaded
shot pick "woma pick"

# woma list.
scene sh -c "printf '\$ woma list ~/code --safe\n'; '$BIN' list '$CODE' --safe; sleep 600"
wait_screen "acme-api"
settled
shot list "woma list"
