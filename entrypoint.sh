#!/bin/sh
set -eu

# Set up GitHub blob base URL for PR links
if [ -n "${INPUT_REPO_BLOB_BASE:-}" ]; then
  export SLINKY_REPO_BLOB_BASE_URL="${INPUT_REPO_BLOB_BASE}"
elif [ -n "${GITHUB_REPOSITORY:-}" ]; then
  COMMIT_SHA="${GITHUB_SHA:-}"
  if [ -n "${GITHUB_EVENT_PATH:-}" ] && command -v jq >/dev/null 2>&1; then
    PR_HEAD_SHA="$(jq -r '.pull_request.head.sha // empty' "$GITHUB_EVENT_PATH" 2>/dev/null || true)"
    [ -n "$PR_HEAD_SHA" ] && COMMIT_SHA="$PR_HEAD_SHA"
  fi
  [ -n "$COMMIT_SHA" ] && export SLINKY_REPO_BLOB_BASE_URL="https://github.com/${GITHUB_REPOSITORY}/blob/${COMMIT_SHA}"
fi

# Build slinky command arguments
ARGS="check"

# Optional flags
[ -n "${INPUT_CONCURRENCY:-}" ] && ARGS="$ARGS --concurrency ${INPUT_CONCURRENCY}"
[ -n "${INPUT_TIMEOUT:-}" ] && ARGS="$ARGS --timeout ${INPUT_TIMEOUT}"
[ -n "${INPUT_JSON_OUT:-}" ] && ARGS="$ARGS --json-out ${INPUT_JSON_OUT}"
[ -n "${INPUT_MD_OUT:-}" ] && ARGS="$ARGS --md-out ${INPUT_MD_OUT}"
[ -n "${INPUT_REPO_BLOB_BASE:-}" ] && ARGS="$ARGS --repo-blob-base ${INPUT_REPO_BLOB_BASE}"

# Boolean flags with defaults
[ "${INPUT_FAIL_ON_FAILURES:-true}" = "true" ] && ARGS="$ARGS --fail-on-failures=true" || ARGS="$ARGS --fail-on-failures=false"
[ "${INPUT_RESPECT_GITIGNORE:-true}" = "true" ] && ARGS="$ARGS --respect-gitignore=true" || ARGS="$ARGS --respect-gitignore=false"

# Add targets (comma-separated glob patterns)
if [ -n "${INPUT_TARGETS:-}" ]; then
  IFS=','
  for target in $INPUT_TARGETS; do
    target=$(echo "$target" | xargs)
    [ -n "$target" ] && ARGS="$ARGS $target"
  done
  unset IFS
else
  ARGS="$ARGS **/*"
fi

# Debug output
[ "${ACTIONS_STEP_DEBUG:-}" = "true" ] && printf "::debug:: Running: slinky %s\n" "$ARGS"

# Execute slinky (crawl repo with glob input, filter via .slinkignore)
slinky $ARGS
EXIT_CODE=$?

# Expose outputs
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  [ -n "${INPUT_JSON_OUT:-}" ] && echo "json_path=${INPUT_JSON_OUT}" >> "$GITHUB_OUTPUT"
  [ -n "${INPUT_MD_OUT:-}" ] && echo "md_path=${INPUT_MD_OUT}" >> "$GITHUB_OUTPUT"
fi

# Append report to job summary if requested
if [ "${INPUT_STEP_SUMMARY:-true}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ] && [ -n "${INPUT_MD_OUT:-}" ] && [ -f "${INPUT_MD_OUT}" ]; then
  cat "${INPUT_MD_OUT}" >> "$GITHUB_STEP_SUMMARY"
fi

exit ${EXIT_CODE:-0}