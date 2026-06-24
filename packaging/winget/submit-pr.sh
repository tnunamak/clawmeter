#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: packaging/winget/submit-pr.sh vX.Y.Z [manifest-dir]

Creates or updates the Clawmeter WinGet PR from a generated manifest directory.

Environment:
  GH_TOKEN              GitHub token that can push to the WinGet fork and open PRs
  WINGET_FORK_REPO     Fork repo, default: tnunamak/winget-pkgs
  WINGET_UPSTREAM_REPO Upstream repo, default: microsoft/winget-pkgs
  WINGET_BRANCH        Branch name, default: add-clawmeter-<version>
  WINGET_WORK_ROOT     Scratch root, default: ~/.tmp
  WINGET_DRY_RUN=1     Prepare the branch locally but do not push or open/edit a PR
EOF
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 2
fi

tag="$1"
version="${tag#v}"
package_id="tnunamak.Clawmeter"
manifest_dir="${2:-packaging/winget/out/manifests/t/tnunamak/Clawmeter/${version}}"
fork_repo="${WINGET_FORK_REPO:-tnunamak/winget-pkgs}"
upstream_repo="${WINGET_UPSTREAM_REPO:-microsoft/winget-pkgs}"
fork_owner="${fork_repo%%/*}"
branch="${WINGET_BRANCH:-add-clawmeter-${version}}"
work_root="${WINGET_WORK_ROOT:-${HOME}/.tmp}"
dry_run="${WINGET_DRY_RUN:-0}"

if [[ ! -d "$manifest_dir" ]]; then
  echo "missing manifest directory: $manifest_dir" >&2
  exit 1
fi
manifest_dir="$(cd "$manifest_dir" && pwd)"

if ! command -v gh >/dev/null 2>&1; then
  echo "missing dependency: gh" >&2
  exit 1
fi
if ! command -v git >/dev/null 2>&1; then
  echo "missing dependency: git" >&2
  exit 1
fi
if [[ "$dry_run" != "1" && -z "${GH_TOKEN:-}" ]]; then
  echo "GH_TOKEN is required unless WINGET_DRY_RUN=1" >&2
  exit 1
fi

mkdir -p "$work_root"
workdir="$(mktemp -d "${work_root%/}/winget-pkgs.XXXXXX")"
cleanup() {
  if [[ "${WINGET_KEEP_WORKTREE:-0}" != "1" ]]; then
    rm -rf "$workdir"
  else
    echo "kept worktree: $workdir"
  fi
}
trap cleanup EXIT

if [[ "$dry_run" != "1" ]]; then
  gh auth setup-git >/dev/null
fi

gh repo clone "$fork_repo" "$workdir" -- --filter=blob:none --sparse
cd "$workdir"
git sparse-checkout set manifests/t/tnunamak/Clawmeter
git remote add upstream "https://github.com/${upstream_repo}.git" 2>/dev/null || true
git fetch upstream master --depth=1
git fetch origin "$branch" --depth=1 || true
git checkout -B "$branch" upstream/master

target_parent="manifests/t/tnunamak/Clawmeter"
target_dir="${target_parent}/${version}"
rm -rf "$target_dir"
mkdir -p "$target_parent"
cp -R "$manifest_dir" "$target_dir"

if git ls-tree -d upstream/master "$target_parent" | grep -q "$target_parent"; then
  kind="New version"
else
  kind="New package"
fi
title="${kind}: ${package_id} version ${version}"

git add "$target_dir"
if git diff --cached --quiet; then
  echo "No WinGet manifest changes for ${package_id} ${version}."
  exit 0
else
  git commit -m "$title"
fi

body="$(cat <<EOF
## Package Details

- Package: ${package_id}
- Version: ${version}
- Release: https://github.com/tnunamak/clawmeter/releases/tag/${tag}
- Installer: https://github.com/tnunamak/clawmeter/releases/download/${tag}/ClawmeterSetup.exe

Clawmeter is a system tray and CLI quota meter for AI coding tools.
EOF
)"

if [[ "$dry_run" == "1" ]]; then
  echo "Dry run: would push ${fork_repo}:${branch}"
  echo "Dry run: would create/update PR against ${upstream_repo}:master"
  echo "PR title: ${title}"
  exit 0
fi

git push --force-with-lease origin "$branch"

existing_pr="$(
  gh api "repos/${upstream_repo}/pulls" \
    -X GET \
    -f "head=${fork_owner}:${branch}" \
    -f state=open \
    --jq '.[0].number // empty'
)"

if [[ -n "$existing_pr" ]]; then
  gh pr edit "$existing_pr" --repo "$upstream_repo" --title "$title" --body "$body"
  echo "Updated PR: https://github.com/${upstream_repo}/pull/${existing_pr}"
else
  gh pr create \
    --repo "$upstream_repo" \
    --base master \
    --head "${fork_owner}:${branch}" \
    --title "$title" \
    --body "$body"
fi
