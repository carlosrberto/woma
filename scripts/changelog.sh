#!/bin/bash
#
# Generate a grouped changelog body from Conventional Commits.
#
# Groups commit subjects by type:
#   feat            -> ### Features
#   fix             -> ### Bug Fixes
#   perf            -> ### Performance
#   everything else -> ### Internal improvements
#     (refactor, chore, docs, test, style, build, ci, revert, and any
#      non-conventional subjects)
#
# The `type:`/`type(scope):` prefix is stripped; a scope, when present, is
# surfaced as a bold lead-in.
#
# Usage:
#   scripts/changelog.sh [<git-range>]
#
# With no range, uses commits since the most recent tag, or the full history
# when there are no tags.

set -euo pipefail

range="${1:-}"
if [ -z "$range" ]; then
    last_tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
    [ -n "$last_tag" ] && range="${last_tag}..HEAD"
fi

if [ -n "$range" ]; then
    subjects="$(git log "$range" --no-merges --pretty=format:'%s')"
else
    subjects="$(git log --no-merges --pretty=format:'%s')"
fi

# GitHub linkifies bare @handles in release notes: an @MainActor in a
# commit subject pings the unrelated user "mainactor" and lists them as a
# release contributor. Wrap handles in backticks so they render as code.
escape_mentions() {
    # shellcheck disable=SC2016  # sed backrefs, not shell expansion
    printf '%s' "$1" | sed -E 's/(^|[^`[:alnum:]_])@([[:alnum:]][-[:alnum:]_]*)/\1`@\2`/g'
}

feat=""
fix=""
perf=""
internal=""

while IFS= read -r line; do
    [ -z "$line" ] && continue

    if [[ "$line" =~ ^([a-z]+)(\(([^\)]*)\))?!?:[[:space:]]+(.*)$ ]]; then
        type="${BASH_REMATCH[1]}"
        scope="${BASH_REMATCH[3]}"
        desc="$(escape_mentions "${BASH_REMATCH[4]}")"

        prefix=""
        [ -n "$scope" ] && prefix="**${scope}:** "
        entry="- ${prefix}${desc}"

        case "$type" in
            feat) feat+="${entry}"$'\n' ;;
            fix)  fix+="${entry}"$'\n' ;;
            perf) perf+="${entry}"$'\n' ;;
            *)    internal+="${entry}"$'\n' ;;
        esac
    else
        # Non-conventional subject — treat as an internal note.
        internal+="- $(escape_mentions "$line")"$'\n'
    fi
done <<< "$subjects"

emit() {
    # $1 = heading, $2 = body (may be empty)
    # Always return 0: a falsy `[ -n "$2" ]` test must not abort the script
    # under `set -e`, nor skip the sections emitted after an empty one.
    [ -n "$2" ] && printf '### %s\n\n%s\n' "$1" "$2"
    return 0
}

if [ -z "${feat}${fix}${perf}${internal}" ]; then
    echo "- No notable changes."
else
    emit "Features" "$feat"
    emit "Bug Fixes" "$fix"
    emit "Performance" "$perf"
    emit "Internal improvements" "$internal"
fi
