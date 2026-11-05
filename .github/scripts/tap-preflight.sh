#!/usr/bin/env bash
# A failed tap check selects manual recovery; it must not prevent publishing.
# Never enable xtrace here: GH_TOKEN is supplied only through the environment.
set -u

ready=false
preflight_dir=''
if [[ -z "${GITHUB_OUTPUT:-}" ]]; then
  exit 1
fi
finish() {
  if [[ -n "$preflight_dir" ]]; then
    rm -rf -- "$preflight_dir"
  fi
  printf 'ready=%s\n' "$ready" >> "$GITHUB_OUTPUT"
}
trap finish EXIT

# setup-git writes global configuration. Confine it to an Actions-owned,
# private temporary directory, and refuse to configure a developer's machine.
if [[ "${GITHUB_ACTIONS:-}" != true || -z "${GH_TOKEN:-}" ||
      "${RUNNER_TEMP:-}" != /* || ! -d "${RUNNER_TEMP:-}" ]]; then
  exit 0
fi
for tool in gh git timeout; do
  command -v "$tool" >/dev/null 2>&1 || exit 0
done
preflight_dir="$(mktemp -d "${RUNNER_TEMP%/}/tap-preflight.XXXXXX" 2>/dev/null)" || exit 0
export GH_CONFIG_DIR="$preflight_dir/gh"
export GIT_CONFIG_GLOBAL="$preflight_dir/gitconfig"
export GIT_CONFIG_NOSYSTEM=1
export GIT_TERMINAL_PROMPT=0
export GH_PROMPT_DISABLED=1
export GH_NO_UPDATE_NOTIFIER=1
export GH_NO_EXTENSION_UPDATE_NOTIFIER=1

# Six client commands at at most 15s + 2s forced termination each: no retries,
# and less than two minutes in total. Suppress client diagnostics because they
# may contain authentication details; only the boolean output leaves this check.
metadata="$(timeout --kill-after=2s 15s gh api --hostname github.com \
  repos/alikatgh/homebrew-tap \
  --jq '.default_branch | select(type == "string" and length > 0) | [., @uri] | @tsv' \
  2>/dev/null)" || exit 0
IFS=$'\t' read -r default_branch encoded_branch extra <<< "$metadata"
if [[ "$metadata" == *$'\n'* || -z "$default_branch" ||
      -z "$encoded_branch" || -n "$extra" ]]; then
  exit 0
fi
timeout --kill-after=2s 15s git check-ref-format "refs/heads/$default_branch" \
  >/dev/null 2>&1 || exit 0
unprotected="$(timeout --kill-after=2s 15s gh api --hostname github.com \
  "repos/alikatgh/homebrew-tap/branches/$encoded_branch" \
  --jq '.protected == false' 2>/dev/null)" || exit 0
[[ "$unprotected" == true ]] || exit 0

timeout --kill-after=2s 15s gh auth setup-git --hostname github.com \
  >/dev/null 2>&1 || exit 0
timeout --kill-after=2s 15s git clone --depth 1 --no-checkout \
  --single-branch --branch "$default_branch" -- \
  https://github.com/alikatgh/homebrew-tap.git "$preflight_dir/tap" \
  >/dev/null 2>&1 || exit 0
# The credential helper supplies GH_TOKEN without putting it in a URL or argv.
# --dry-run never updates a remote ref. It cannot promise that later branch
# policy, token validity, or GoReleaser's actual formula push will still succeed.
timeout --kill-after=2s 15s git -C "$preflight_dir/tap" \
  -c core.hooksPath=/dev/null push --dry-run --no-verify origin \
  "HEAD:refs/heads/$default_branch" >/dev/null 2>&1 || exit 0
ready=true
