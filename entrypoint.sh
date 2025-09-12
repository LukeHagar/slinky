#!/bin/sh
set -eu

PATH_ARG="${INPUT_PATH:-.}"
PATTERNS_ARG="${INPUT_PATTERNS:-**/*}"
CONCURRENCY_ARG="${INPUT_CONCURRENCY:-16}"
TIMEOUT_ARG="${INPUT_TIMEOUT:-10}"
RESPECT_GITIGNORE_ARG="${INPUT_RESPECT_GITIGNORE:-true}"
JSON_OUT_ARG="${INPUT_JSON_OUT:-results.json}"
MD_OUT_ARG="${INPUT_MD_OUT:-results.md}"
REPO_BLOB_BASE_ARG="${INPUT_REPO_BLOB_BASE:-}"
FAIL_ON_FAILURES_ARG="${INPUT_FAIL_ON_FAILURES:-true}"
COMMENT_PR_ARG="${INPUT_COMMENT_PR:-true}"
STEP_SUMMARY_ARG="${INPUT_STEP_SUMMARY:-true}"

# Build argv safely
set -- check --concurrency "$CONCURRENCY_ARG" --timeout "$TIMEOUT_ARG"
if [ "${FAIL_ON_FAILURES_ARG}" = "true" ]; then
  set -- "$@" --fail-on-failures=true
else
  set -- "$@" --fail-on-failures=false
fi

if [ -n "${PATTERNS_ARG}" ]; then
  set -- "$@" "$PATTERNS_ARG"
else
  set -- "$@" "**/*"
fi

if [ -n "${JSON_OUT_ARG}" ]; then
  set -- "$@" --json-out "$JSON_OUT_ARG"
fi
if [ -n "${MD_OUT_ARG}" ]; then
  set -- "$@" --md-out "$MD_OUT_ARG"
fi
if [ "${RESPECT_GITIGNORE_ARG}" = "true" ]; then
  set -- "$@" --respect-gitignore=true
else
  set -- "$@" --respect-gitignore=false
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

# Emit consolidated config at start (visible with ACTIONS_STEP_DEBUG=true)
EFFECTIVE_REPO_BLOB_BASE="${SLINKY_REPO_BLOB_BASE_URL:-$REPO_BLOB_BASE_ARG}"
TARGETS_DEBUG="${PATTERNS_ARG:-**/*}"
printf "::debug:: Config: targets=%s concurrency=%s timeout=%s respect_gitignore=%s json_out=%s md_out=%s fail_on_failures=%s comment_pr=%s step_summary=%s repo_blob_base_url=%s\n" \
  "$TARGETS_DEBUG" "$CONCURRENCY_ARG" "$TIMEOUT_ARG" "$RESPECT_GITIGNORE_ARG" "$JSON_OUT_ARG" "$MD_OUT_ARG" \
  "$FAIL_ON_FAILURES_ARG" "$COMMENT_PR_ARG" "$STEP_SUMMARY_ARG" "$EFFECTIVE_REPO_BLOB_BASE"
printf "::debug:: CLI Args: slinky %s\n" "$*"

# Execute but always continue to allow summaries/comments even on failure
set +e
slinky "$@"
SLINKY_EXIT_CODE=$?
set -e

# Expose outputs (use underscore names)
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  if [ -n "${JSON_OUT_ARG}" ]; then
    echo "json_path=${JSON_OUT_ARG}" >> "$GITHUB_OUTPUT"
  fi
  if [ -n "${MD_OUT_ARG}" ]; then
    echo "md_path=${MD_OUT_ARG}" >> "$GITHUB_OUTPUT"
  fi
fi

# Append report to job summary if requested
if [ "${STEP_SUMMARY_ARG}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ] && [ -n "${MD_OUT_ARG}" ] && [ -f "${MD_OUT_ARG}" ]; then
  cat "${MD_OUT_ARG}" >> "$GITHUB_STEP_SUMMARY"
fi

# Post PR comment if this is a PR and requested (even if the run failed)
if [ "${COMMENT_PR_ARG}" = "true" ]; then
  if [ -z "${MD_OUT_ARG}" ] || [ ! -f "${MD_OUT_ARG}" ]; then
    echo "[slinky] No markdown report found at '${MD_OUT_ARG}', skipping PR comment."
  else
    PR_NUMBER=""
    if [ -n "${GITHUB_EVENT_PATH:-}" ] && command -v jq >/dev/null 2>&1; then
      PR_NUMBER="$(jq -r '.pull_request.number // empty' "$GITHUB_EVENT_PATH" || true)"
    fi
    if [ -z "${PR_NUMBER}" ]; then
      echo "[slinky] Not a pull_request event or PR number not found; skipping PR comment."
    elif [ -z "${GITHUB_TOKEN:-}" ]; then
      echo "[slinky] GITHUB_TOKEN not available; ensure the workflow grants permissions and passes the token as env. Skipping PR comment."
    elif [ -z "${GITHUB_REPOSITORY:-}" ]; then
      echo "[slinky] GITHUB_REPOSITORY not set; skipping PR comment."
    else
      BODY_CONTENT="$(cat "${MD_OUT_ARG}")"
      COMMENT_BODY="$(printf '%s\n%s\n' '<!-- slinky-report -->' "${BODY_CONTENT}")"

      # Try to find an existing slinky comment to update
      COMMENTS_JSON=$(curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
           -H "Accept: application/vnd.github+json" \
           -H "X-GitHub-Api-Version: 2022-11-28" \
           "https://api.github.com/repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments?per_page=100") || COMMENTS_JSON="[]"

      EXISTING_ID=$(printf '%s' "${COMMENTS_JSON}" | jq -r '[.[] | select((.body // "") | contains("<!-- slinky-report -->"))][0].id // empty')

      if [ -n "${EXISTING_ID}" ]; then
        # Update existing comment
        curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
             -H "Accept: application/vnd.github+json" \
             -H "X-GitHub-Api-Version: 2022-11-28" \
             -X PATCH "https://api.github.com/repos/${GITHUB_REPOSITORY}/issues/comments/${EXISTING_ID}" \
             -d "$(printf '{"body": %s}' "$(jq -Rs . <<EOF
${COMMENT_BODY}
EOF
)" )" >/dev/null || true
        echo "[slinky] Updated existing PR comment #${EXISTING_ID}."
      else
        # Create new comment
        curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
             -H "Accept: application/vnd.github+json" \
             -H "X-GitHub-Api-Version: 2022-11-28" \
             -X POST "https://api.github.com/repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments" \
             -d "$(printf '{"body": %s}' "$(jq -Rs . <<EOF
${COMMENT_BODY}
EOF
)" )" >/dev/null || true
        echo "[slinky] Posted PR comment to #${PR_NUMBER}."
      fi
    fi
  fi
fi

exit ${SLINKY_EXIT_CODE:-0}

