#!/usr/bin/env bash

set -euo pipefail

repository_root="$(git rev-parse --show-toplevel)"
cd "$repository_root"

check_history=false
if [[ "${1:-}" == "--history" ]]; then
  check_history=true
elif [[ $# -gt 0 ]]; then
  echo "usage: $0 [--history]" >&2
  exit 2
fi

failed=false

report_matches() {
  local heading="$1"
  local matches="$2"
  if [[ -n "$matches" ]]; then
    echo "$heading" >&2
    printf '%s\n' "$matches" >&2
    failed=true
  fi
}

snapshot_paths="$(
  git ls-files --cached --others --exclude-standard |
    while IFS= read -r path; do
      [[ -e "$path" || -L "$path" ]] && printf '%s\n' "$path"
    done
)"

forbidden_paths="$(
  printf '%s\n' "$snapshot_paths" |
    grep -E '(^|/)(__pycache__|logs?|temp|tmp)(/|$)|^(research|mcp)/|\.py[co]$|\.log$|\.prof$|\.map$|^spec/specgen/specgen$|^\.claude/settings\.local\.json$' ||
    true
)"
report_matches "Release-inappropriate paths are present:" "$forbidden_paths"

private_references="$(
  while IFS= read -r path; do
    [[ "$path" == "scripts/release-hygiene.sh" ]] && continue
    grep -InH -I -E \
      '(/root/(prometheus|matrix|machine-genome)(/|$)|147\.93\.139\.18|Gideon)' \
      -- "$path" 2>/dev/null ||
      true
  done <<< "$snapshot_paths"
)"
report_matches "Machine-specific or private infrastructure references are present:" "$private_references"

credential_material="$(
  while IFS= read -r path; do
    case "$path" in
      internal/project/intelligence_test.go | scripts/release-hygiene.sh)
        continue
        ;;
    esac
    grep -InH -I -E \
      '(-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36,}|sk-proj-[A-Za-z0-9_-]{20,})' \
      -- "$path" 2>/dev/null ||
      true
  done <<< "$snapshot_paths"
)"
report_matches "Possible credential material is present:" "$credential_material"

if $check_history; then
  historical_paths="$(
    git rev-list --objects --all |
      sed -n 's/^[0-9a-f]\{40,\} //p' |
      grep -E '(^|/)(__pycache__|logs?|temp|tmp)(/|$)|^(research|mcp)/|\.py[co]$|\.log$|\.prof$|\.map$|^spec/specgen/specgen$' ||
      true
  )"
  report_matches "Release-inappropriate paths exist in reachable history:" "$historical_paths"

  historical_references="$(
    while IFS= read -r commit; do
      git grep -nI -E \
        '(/root/(prometheus|matrix|machine-genome)(/|$)|147\.93\.139\.18|Gideon)' \
        "$commit" -- . ':(exclude)scripts/release-hygiene.sh' 2>/dev/null ||
        true
    done < <(git rev-list --all)
  )"
  report_matches "Private references exist in reachable history:" "$historical_references"
fi

if $failed; then
  echo "release hygiene: failed" >&2
  exit 1
fi

scope="current release snapshot"
$check_history && scope="current release snapshot and reachable history"
echo "release hygiene: passed ($scope)"
