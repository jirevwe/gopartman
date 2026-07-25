#!/usr/bin/env bash
# release.sh — bump the library version by pushing a new git tag.
#
# Usage:
#   scripts/release.sh patch              # 0.1.0 → 0.1.1
#   scripts/release.sh minor              # 0.1.0 → 0.2.0
#   scripts/release.sh major              # 0.1.0 → 1.0.0
#   scripts/release.sh 0.2.0              # explicit version
#   scripts/release.sh --dry-run patch    # show plan, don't tag or push
#   scripts/release.sh --yes patch        # skip the push confirmation
#
# What this does:
#   1. Reads the current version from `git describe --tags --abbrev=0`
#      (falls back to 0.0.0 if the repo has no tags yet).
#   2. Computes the new version (semver bump OR explicit X.Y.Z).
#   3. Verifies pre-flight: on `main`, clean working tree, tag doesn't
#      already exist locally or on origin, remote is up to date.
#   4. Creates an annotated git tag `vX.Y.Z` at HEAD.
#   5. Pushes the tag (after a y/N confirmation, unless --yes).
#
# gopartman is a Go module — the git tag is the version. There is no
# file to edit and no version-bump commit; consumers pin via `go get`.

set -euo pipefail

# ── Locate the repo root regardless of where the script is invoked ──
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

DRY_RUN=false
ASSUME_YES=false

# ── Parse args ──────────────────────────────────────────────────────
BUMP_OR_VERSION=""
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        --yes|-y)  ASSUME_YES=true ;;
        -h|--help)
            # Print the comment header at the top of this script (everything
            # between the shebang and the `set -euo pipefail` line). Avoids
            # any `head -n -N` form — BSD `head` (macOS) doesn't take
            # negative counts.
            awk '/^set -/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "$0"
            exit 0
            ;;
        *)
            if [[ -n "$BUMP_OR_VERSION" ]]; then
                echo "error: extra positional arg '$arg'" >&2
                exit 2
            fi
            BUMP_OR_VERSION="$arg"
            ;;
    esac
done

if [[ -z "$BUMP_OR_VERSION" ]]; then
    echo "error: missing required arg (patch|minor|major|X.Y.Z)" >&2
    echo "       run with --help for usage" >&2
    exit 2
fi

# ── Read current version from the latest git tag ────────────────────
# `git describe --tags --abbrev=0` returns the most recent tag reachable
# from HEAD. Strip a leading `v` if present so semver math is uniform.
CURRENT_TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
if [[ -z "$CURRENT_TAG" ]]; then
    CURRENT_VERSION="0.0.0"
else
    CURRENT_VERSION="${CURRENT_TAG#v}"
fi

# ── Compute new version ─────────────────────────────────────────────
if [[ "$BUMP_OR_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    NEW_VERSION="$BUMP_OR_VERSION"
elif [[ "$BUMP_OR_VERSION" == "patch" || "$BUMP_OR_VERSION" == "minor" || "$BUMP_OR_VERSION" == "major" ]]; then
    if [[ ! "$CURRENT_VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
        echo "error: current version '$CURRENT_VERSION' isn't valid semver — can't bump." >&2
        echo "       set an explicit version instead: $0 X.Y.Z" >&2
        exit 1
    fi
    MAJOR="${BASH_REMATCH[1]}"
    MINOR="${BASH_REMATCH[2]}"
    PATCH="${BASH_REMATCH[3]}"
    case "$BUMP_OR_VERSION" in
        patch) PATCH=$((PATCH + 1)) ;;
        minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
        major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
    esac
    NEW_VERSION="${MAJOR}.${MINOR}.${PATCH}"
else
    echo "error: '$BUMP_OR_VERSION' isn't a valid bump type or version." >&2
    echo "       use patch|minor|major or X.Y.Z" >&2
    exit 2
fi

NEW_TAG="v${NEW_VERSION}"

echo "Current version: $CURRENT_VERSION"
echo "New version:     $NEW_VERSION"
echo "Tag:             $NEW_TAG"
echo

# ── Pre-flight checks ───────────────────────────────────────────────
# Branch — release only from `main`. Override-able via env if a hotfix
# branch ever needs to ship, but the default refuses.
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$CURRENT_BRANCH" != "main" && "${ALLOW_NON_MAIN:-0}" != "1" ]]; then
    echo "error: not on main (currently on $CURRENT_BRANCH)." >&2
    echo "       to release from a non-main branch: ALLOW_NON_MAIN=1 $0 $*" >&2
    exit 1
fi

# Tag uniqueness — check both local and remote. Always runs because
# duplicates would block the eventual real release.
if git rev-parse "$NEW_TAG" >/dev/null 2>&1; then
    echo "error: local tag $NEW_TAG already exists." >&2
    exit 1
fi
if git ls-remote --tags origin "refs/tags/$NEW_TAG" 2>/dev/null | grep -q "$NEW_TAG"; then
    echo "error: tag $NEW_TAG already exists on origin." >&2
    exit 1
fi

# Skip the rest of pre-flight on --dry-run — those guards exist to
# protect a destructive action, and dry-run doesn't perform one.
if ! $DRY_RUN; then
    # Clean working tree.
    if [[ -n "$(git status --porcelain)" ]]; then
        echo "error: working tree not clean. Commit or stash changes first." >&2
        git status --short >&2
        exit 1
    fi

    # Remote sync — refuse if main is behind / has diverged.
    echo "Fetching origin..."
    git fetch --quiet origin
    LOCAL_SHA="$(git rev-parse HEAD)"
    REMOTE_SHA="$(git rev-parse "origin/$CURRENT_BRANCH")"
    if [[ "$LOCAL_SHA" != "$REMOTE_SHA" ]]; then
        # Check if local is ahead (we have commits to push first) vs behind.
        if git merge-base --is-ancestor "$REMOTE_SHA" "$LOCAL_SHA"; then
            echo "error: local is ahead of origin/$CURRENT_BRANCH. Push your commits first." >&2
            exit 1
        else
            echo "error: origin/$CURRENT_BRANCH has commits you don't have locally. Pull first." >&2
            exit 1
        fi
    fi
fi

if $DRY_RUN; then
    echo "Dry run — no tag created, no push."
    exit 0
fi

# ── Tag ─────────────────────────────────────────────────────────────
echo "Tagging:"
git tag -a "$NEW_TAG" -m "Release ${NEW_TAG}"
echo "  tag: $NEW_TAG at $(git rev-parse HEAD)"

# ── Confirm + push ──────────────────────────────────────────────────
echo
echo "About to push:"
echo "  • tag  $NEW_TAG  →  origin"
echo

if ! $ASSUME_YES; then
    read -r -p "Push? [y/N] " REPLY
    if [[ ! "$REPLY" =~ ^[Yy]$ ]]; then
        echo
        echo "Aborted. The tag exists locally; to undo:"
        echo "  git tag -d $NEW_TAG"
        echo "Or push later with:"
        echo "  git push origin $NEW_TAG"
        exit 0
    fi
fi

git push origin "$NEW_TAG"

echo
echo "Pushed. The release will be visible at:"
echo "  https://github.com/jirevwe/gopartman/releases/tag/$NEW_TAG"
