# Releasing

A release is a tag, a GitHub release with notes from
[CHANGELOG.md](../CHANGELOG.md) and binaries for macOS and Linux (arm64,
amd64) with `checksums.txt`, and the Homebrew formula in
[carlosrberto/homebrew-tap](https://github.com/carlosrberto/homebrew-tap).
`make` does all of it.

The binaries aren't signed by Apple. Homebrew doesn't quarantine what it
downloads, so the formula installs them and macOS runs them; Go gives
them the ad-hoc signature Apple silicon needs. Only a binary downloaded
with a browser is refused (the README has the workaround). `--HEAD`
installs build `main` from source.

## Once

1. Clone the tap next to this repo: `gh repo clone carlosrberto/homebrew-tap`
   (or set `TAP=path/to/it`). Both repos must be public: a private repo's
   release downloads need a token, so `brew install` fails.
2. `make setup`: the pre-push hook runs the tests on every push.
3. `gh` logged in with the `carlosrberto` account (`gh auth login`).
   Releases use it whichever account is active; `GH_USER=other` changes
   it, `GH_USER=` uses the active one.

## A release

```sh
make bump-patch    # or bump-minor, bump-major; skip it for the first release (0.1.0)
make prepare       # checks, adds the CHANGELOG.md section, commits "chore(release): vX"
make release       # builds, tags, pushes, creates the GitHub release, updates the formula
```

- `make bump-*` sets `version` in `main.go`.
- `make prepare` runs `make check`, then writes the CHANGELOG.md section
  from the commits since the last tag, grouped by their conventional
  commit type (`make release-notes` shows it beforehand). Edit the
  section before releasing if you like: amend the commit.
- `make release` refuses unless HEAD is the release commit, the tree is
  clean and the tag is new. `make dist` builds the archives into `dist/`;
  one push sends main and the annotated tag (so the tests run once); `gh
  release create` uploads the archives with the notes; then
  `scripts/formula.sh` writes `Formula/woma.rb` into the tap, with each
  archive's URL and sha256 from `dist/checksums.txt`, which is committed
  and pushed.

Then `brew upgrade woma` (or `brew install carlosrberto/tap/woma`) and
`brew test woma` try it; `brew audit --strict carlosrberto/tap/woma`
checks the formula.

`make undo-release` undoes the last one: deletes the GitHub release and
the tag, drops the release commit (force-pushing main with a lease), and
reverts the formula in the tap.

`make formula` prints the formula for the current version's tag, without
writing anything.
