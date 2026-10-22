#!/bin/sh
# Publishes the version `make prepare` committed: tags it, pushes, creates
# the GitHub release with notes from CHANGELOG.md, then writes the Homebrew
# formula into the tap and pushes it. Run by `make release`. The release
# has no binaries: unsigned ones are refused by macOS, and Homebrew and go
# install build from source.
#
#   TAP      the tap's clone (default ../homebrew-tap)
#   GH_USER  the gh account to publish with (default carlosrberto; empty
#            for gh's active account)
set -eu

cd "$(dirname "$0")/.."

version=$(sed -n 's/^var version = "\(.*\)"$/\1/p' main.go)
tag="v$version"
tap="${TAP:-../homebrew-tap}"
gh_user="${GH_USER-carlosrberto}"

fail() {
	echo "release: $*" >&2
	exit 1
}

[ -n "$version" ] || fail "no version in main.go"
[ "$(git branch --show-current)" = main ] || fail "not on main"
[ -z "$(git status --porcelain)" ] || fail "uncommitted changes"
[ "$(git log -1 --format=%s)" = "chore(release): $tag" ] || fail "HEAD isn't 'chore(release): $tag' - run 'make prepare' first"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git ls-remote --exit-code --tags origin "$tag" >/dev/null; then
	fail "$tag exists already"
fi
notes=$(awk -v v="$version" 'index($0, "## [" v "]") == 1 {f=1; next} /^## \[/ {f=0} f' CHANGELOG.md)
[ -n "$notes" ] || fail "no CHANGELOG.md section for $version - run 'make prepare' first"

[ -d "$tap/.git" ] || fail "no tap at $tap (gh repo clone carlosrberto/homebrew-tap $tap, or set TAP)"
[ -z "$(git -C "$tap" status --porcelain)" ] || fail "uncommitted changes in $tap"
git -C "$tap" pull -q --ff-only

if [ -n "$gh_user" ]; then
	GH_TOKEN=$(gh auth token --user "$gh_user") || fail "gh has no token for $gh_user (gh auth login, or GH_USER=)"
	export GH_TOKEN
fi

# One push for main and the tag, so the pre-push tests run once.
git tag -a "$tag" -m "Release $tag"
git push --follow-tags origin main

gh release create "$tag" --title "$tag" --notes "$notes"

echo "release: writing the formula into $tap"
mkdir -p "$tap/Formula"
scripts/formula.sh "$tag" >"$tap/Formula/woma.rb"
git -C "$tap" add Formula/woma.rb
git -C "$tap" commit -qm "woma $version"
git -C "$tap" push -q

echo "release: published $tag - brew upgrade woma (or brew install carlosrberto/tap/woma)"
