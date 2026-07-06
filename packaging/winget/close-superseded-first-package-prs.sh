#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: packaging/winget/close-superseded-first-package-prs.sh [options]

Close stale open "New package" PRs after Clawmeter has been accepted into WinGet.

Options:
  --latest-tag TAG       Release tag to mention in the close comment
  --current-pr NUMBER    PR number to leave open if it appears in the query
  --dry-run              Print what would close without changing GitHub
  --help                 Show this help

Environment:
  GH_TOKEN              GitHub token with permission to close PRs
  WINGET_FORK_REPO     Fork repo, default: tnunamak/winget-pkgs
  WINGET_UPSTREAM_REPO Upstream repo, default: microsoft/winget-pkgs
EOF
}

package_id="tnunamak.Clawmeter"
target_parent="manifests/t/tnunamak/Clawmeter"
fork_repo="${WINGET_FORK_REPO:-tnunamak/winget-pkgs}"
upstream_repo="${WINGET_UPSTREAM_REPO:-microsoft/winget-pkgs}"
fork_owner="${fork_repo%%/*}"
latest_tag=""
current_pr=""
dry_run="${WINGET_DRY_RUN:-0}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --latest-tag) latest_tag="${2:?--latest-tag requires a value}"; shift 2 ;;
    --current-pr) current_pr="${2:?--current-pr requires a value}"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

have() {
  command -v "$1" >/dev/null 2>&1
}

package_exists_upstream() {
  gh api "repos/${upstream_repo}/contents/${target_parent}" >/dev/null 2>&1
}

open_first_package_prs() {
  gh pr list \
    --repo "$upstream_repo" \
    --state open \
    --limit 100 \
    --search "\"New package: ${package_id} version\" in:title author:${fork_owner}" \
    --json number,title,url,author \
    --jq '.[] | select(.title | startswith("New package: '"${package_id}"' version ")) | [.number, .title, .url, .author.login] | @tsv'
}

have gh || {
  echo "missing dependency: gh" >&2
  exit 1
}

if [[ "$dry_run" != "1" && -z "${GH_TOKEN:-}" ]]; then
  echo "GH_TOKEN is required unless --dry-run or WINGET_DRY_RUN=1 is set" >&2
  exit 1
fi

if ! package_exists_upstream; then
  echo "Skipping superseded PR cleanup: ${package_id} is not accepted upstream yet."
  exit 0
fi

mapfile -t prs < <(open_first_package_prs)
if [[ "${#prs[@]}" -eq 0 ]]; then
  echo "No open superseded first-package PRs found for ${package_id}."
  exit 0
fi

release_sentence=""
if [[ -n "$latest_tag" ]]; then
  release_sentence=" Latest release: https://github.com/tnunamak/clawmeter/releases/tag/${latest_tag}."
fi

for row in "${prs[@]}"; do
  IFS=$'\t' read -r number title url author <<<"$row"
  if [[ -n "$current_pr" && "$number" == "$current_pr" ]]; then
    echo "Skipping current PR #${number}: ${title}"
    continue
  fi
  if [[ "$author" != "$fork_owner" ]]; then
    echo "Skipping PR #${number}: author ${author} is not ${fork_owner}."
    continue
  fi

  echo "Closing superseded first-package PR #${number}: ${title}"
  if [[ "$dry_run" == "1" ]]; then
    echo "Dry run: would close ${url}"
    continue
  fi

  gh pr close "$number" \
    --repo "$upstream_repo" \
    --comment "Closing as superseded because ${package_id} is now accepted in WinGet. Future releases should use New version PRs.${release_sentence}"
done
