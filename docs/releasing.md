# Releasing

How a woma release is made: binaries for macOS and Linux (amd64, arm64)
with checksums on GitHub, the Homebrew formula in
[carlosrberto/homebrew-tap](https://github.com/carlosrberto/homebrew-tap),
and `go install` working from the module. [.goreleaser.yaml](../.goreleaser.yaml)
is the binaries' configuration; [scripts/formula.sh](../scripts/formula.sh)
writes the formula.

Homebrew builds woma from the tag's source archive (it brings Go to build
it): a binary built on the machine isn't quarantined, so macOS runs it
without it being signed or notarized. The release's binaries are for
Linux, and for direct downloads.

## Once

1. **The repo**: woma on GitHub, public. A private repo's source archives
   need a token, so `brew install` fails, and `go install` can't fetch
   the module.

   ```sh
   gh repo create carlosrberto/woma --public --source . --push
   ```

2. **The tap**: `carlosrberto/homebrew-tap`, public, shared with other
   tools (tux's formula goes there too), cloned next to this one. The
   formula is `Formula/woma.rb` in it; `brew install
   carlosrberto/tap/woma` finds it there (the `homebrew-` prefix is what
   makes the short name work).

   ```sh
   cd .. && gh repo create carlosrberto/homebrew-tap --public --clone
   mkdir -p homebrew-tap/Formula
   ```

   If it exists already: `gh repo clone carlosrberto/homebrew-tap` next
   to this repo.
3. **goreleaser** (only for the release's binaries): `brew install
   goreleaser`. `goreleaser check` validates the config.

## A release

1. `go test -race ./...` and `go vet ./...`.
2. Set `version` in `main.go` to the new version (it's what a
   `go install` build says), commit.
3. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`.
4. `GITHUB_TOKEN=... goreleaser release --clean`: it runs the tests,
   builds, and writes the release - as a **draft**.
5. Look at the draft on GitHub (the changelog is grouped from the commits'
   types), then publish it.
6. The formula, for the tag (its source archive exists once the tag is
   pushed):

   ```sh
   scripts/formula.sh v0.1.0 > ../homebrew-tap/Formula/woma.rb
   cd ../homebrew-tap && git add Formula/woma.rb && git commit -m "woma 0.1.0" && git push
   ```

   `brew install carlosrberto/tap/woma` then `brew test woma` try it;
   `brew audit --strict carlosrberto/tap/woma` checks the formula.

`goreleaser release --snapshot --clean --skip=publish` does all of it into
`dist/` without a tag or publishing anything - to try the config.
