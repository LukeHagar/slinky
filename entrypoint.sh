#!/bin/sh
set -eu

PATH_ARG="${INPUT_PATH:-.}"
PATTERNS_ARG="${INPUT_PATTERNS:-**/*}"
CONCURRENCY_ARG="${INPUT_CONCURRENCY:-16}"
TIMEOUT_ARG="${INPUT_TIMEOUT:-10}"
JSON_OUT_ARG="${INPUT_JSON_OUT:-results.json}"
MD_OUT_ARG="${INPUT_MD_OUT:-results.md}"
REPO_BLOB_BASE_ARG="${INPUT_REPO_BLOB_BASE:-}"
FAIL_ON_FAILURES_ARG="${INPUT_FAIL_ON_FAILURES:-true}"
COMMENT_PR_ARG="${INPUT_COMMENT_PR:-true}"
STEP_SUMMARY_ARG="${INPUT_STEP_SUMMARY:-true}"

ARGS="check \"${PATH_ARG}\" --concurrency ${CONCURRENCY_ARG} --timeout ${TIMEOUT_ARG}"
if [ "${FAIL_ON_FAILURES_ARG}" = "true" ]; then
  ARGS="$ARGS --fail-on-failures true"
else
  ARGS="$ARGS --fail-on-failures false"
fi
if [ -n "${PATTERNS_ARG}" ]; then
  NORM_PATTERNS=$(printf "%s" "${PATTERNS_ARG}" | sed 's/,\s*/,/g')
  IFS=','
  set -- $NORM_PATTERNS
  unset IFS
  for pat in "$@"; do
    ARGS="$ARGS --patterns \"$pat\""
  done
fi
if [ -n "${JSON_OUT_ARG}" ]; then
  ARGS="$ARGS --json-out \"${JSON_OUT_ARG}\""
fi
if [ -n "${MD_OUT_ARG}" ]; then
  ARGS="$ARGS --md-out \"${MD_OUT_ARG}\""
fi

# Compute GitHub blob base URL for file links used in the Markdown report
if [ -n "${REPO_BLOB_BASE_ARG}" ]; then
  export SLINKY_REPO_BLOB_BASE_URL="${REPO_BLOB_BASE_ARG}"
elif [ -n "${GITHUB_REPOSITORY:-}" ]; then
  COMMIT_SHA="${GITHUB_SHA:-}"
  if [ -n "${GITHUB_EVENT_PATH:-}" ] && command -v jq >/dev/null 2>&1; then
    PR_HEAD_SHA="$(jq -r '.pull_request.head.sha // empty' "$GITHUB_EVENT_PATH" || true)"
    if [ -n "$PR_HEAD_SHA" ]; then
      COMMIT_SHA="$PR_HEAD_SHA"
    fi
  fi
  if [ -n "$COMMIT_SHA" ]; then
    export SLINKY_REPO_BLOB_BASE_URL="https://github.com/${GITHUB_REPOSITORY}/blob/${COMMIT_SHA}"
  fi
fi

eval slinky ${ARGS}

# Expose outputs
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  if [ -n "${JSON_OUT_ARG}" ]; then
    echo "json-path=${JSON_OUT_ARG}" >> "$GITHUB_OUTPUT"
  fi
  if [ -n "${MD_OUT_ARG}" ]; then
    echo "md-path=${MD_OUT_ARG}" >> "$GITHUB_OUTPUT"
  fi
fi

# Append report to job summary if requested
if [ "${STEP_SUMMARY_ARG}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ] && [ -n "${MD_OUT_ARG}" ] && [ -f "${MD_OUT_ARG}" ]; then
  cat "${MD_OUT_ARG}" >> "$GITHUB_STEP_SUMMARY"
fi

# Post PR comment if this is a PR and requested
if [ "${COMMENT_PR_ARG}" = "true" ] && [ -n "${MD_OUT_ARG}" ] && [ -f "${MD_OUT_ARG}" ]; then
  PR_NUMBER=""
  if [ -n "${GITHUB_EVENT_PATH:-}" ] && command -v jq >/dev/null 2>&1; then
    PR_NUMBER="$(jq -r '.pull_request.number // empty' "$GITHUB_EVENT_PATH" || true)"
  fi
  if [ -n "${PR_NUMBER}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_TOKEN:-}" ]; then
    BODY_CONTENT="$(cat "${MD_OUT_ARG}")"
    curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
         -H "Accept: application/vnd.github+json" \
         -H "X-GitHub-Api-Version: 2022-11-28" \
         -X POST "https://api.github.com/repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments" \
         -d "$(printf '{"body": %s}' "$(jq -Rs . <<EOF
${BODY_CONTENT}
EOF
)" )" >/dev/null || true
  fi
fi


