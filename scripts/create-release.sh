#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Create and push an agent release tag.

Usage:
  $0 VERSION [-m MESSAGE] [--dry-run]

VERSION may be "2.0.20" or "agent/v2.0.20".

Pushing the tag triggers GitHub Actions to build release binaries,
publish a GitHub release, and push Docker images to GHCR.

Examples:
  $0 2.0.20
  $0 agent/v2.0.20 -m "Fix disk metrics on ZFS"
  $0 2.0.20 --dry-run
EOF
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

version=""
message=""
dry_run=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -m)
      [[ $# -ge 2 ]] || die "missing value for -m"
      message="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=true
      shift
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      [[ -z "$version" ]] || die "unexpected argument: $1"
      version="$1"
      shift
      ;;
  esac
done

[[ -n "$version" ]] || { usage >&2; exit 1; }

version="${version#agent/v}"
tag="agent/v${version}"

[[ "$tag" =~ ^agent/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
  || die "invalid version: expected MAJOR.MINOR.PATCH (e.g. 2.0.20)"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! git diff --quiet || ! git diff --cached --quiet; then
  die "working tree is not clean; commit or stash changes first"
fi

if git rev-parse -q --verify "refs/tags/${tag}" >/dev/null; then
  die "tag already exists: ${tag}"
fi

latest="$(git tag -l 'agent/v*' --sort=-v:refname | head -n1 || true)"
commit="$(git rev-parse --short HEAD)"

printf 'Tag:     %s\n' "$tag"
printf 'Commit:  %s\n' "$commit"
[[ -n "$latest" ]] && printf 'Latest:  %s\n' "$latest"
printf 'Remote:  origin\n'

if [[ -z "$message" ]]; then
  message="Release ${version}"
fi

if [[ "$dry_run" == true ]]; then
  printf '\nDry run — no tag created.\n'
  exit 0
fi

read -r -p "Create and push ${tag}? [y/N] " confirm
[[ "$confirm" == [yY] || "$confirm" == [yY][eE][sS] ]] || die "aborted"

git tag -a "$tag" -m "$message"
git push origin "$tag"

printf '\nPushed %s. GitHub Actions will build binaries and publish the release.\n' "$tag"
