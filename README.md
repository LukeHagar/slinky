## Slinky Link Checker

Validate external links across your repository. Ships as a self-contained GitHub Action (Docker) and a CLI.

### Quick start (GitHub Action)

Add a workflow:

```yaml
name: Slinky
on:
  pull_request:
    branches: [ main ]
jobs:
  slinky:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - name: Run Slinky
        uses: LukeHagar/slinky@v1
```

### Inputs

- **path**: Root path to scan. Default: `.`
- **patterns**: Comma-separated doublestar patterns (e.g. `docs/**/*.md,**/*.go`). Default: `**/*`
- **concurrency**: Max concurrent requests. Default: `16`
- **timeout**: HTTP timeout seconds. Default: `10`
- **json-out**: Optional JSON results path. Default: `results.json`
- **md-out**: Optional Markdown report path. Default: `results.md`
- **repo-blob-base**: Override GitHub blob base URL (`https://github.com/<owner>/<repo>/blob/<sha>`). Auto-detected in Actions.
- **fail-on-failures**: Fail job on any broken links. Default: `true`
- **comment-pr**: Post Markdown as a PR comment when applicable. Default: `true`
- **step-summary**: Append report to the job summary. Default: `true`

### Output links in PRs

When running on PRs, Slinky auto-links files using the PR head commit. You can override with `repo-blob-base`.

### CLI

Install (from source):

```bash
go build -o slinky ./
```

Usage:

```bash
# Headless: provide one or more targets (files, dirs, or globs)
slinky check **/*
slinky check ./docs/**/* ./markdown/**/*

# TUI mode: same targets
slinky run **/*
```

Notes:
- Targets can be files, directories, or doublestar globs. Multiple targets are allowed.
- If no targets are provided, the default is `**/*` relative to the current working directory.
- Legacy flags `--glob` and `--patterns` are still supported, but positional targets are preferred.

### Notes

- Respects `.gitignore`.
- Skips likely binary files and files > 2 MiB.
- Uses a browser-like User-Agent to reduce false negatives.

### .slinkignore

Place a `.slinkignore` file at the repository root to exclude paths and/or specific URLs from scanning and reporting. The format is JSON with two optional arrays:

```json
{
  "ignorePaths": [
    "**/vendor/**",
    "**/*.bak"
  ],
  "ignoreURLs": [
    "https://example.com/this/path/does/not/exist",
    "*localhost:*",
    "*internal.example.com*"
  ]
}
```

- ignorePaths: gitignore-style patterns evaluated against repository-relative paths (uses doublestar `**`).
- ignoreURLs: patterns applied to the full URL string. Supports exact matches, substring contains, and doublestar-style wildcard matches.

Examples:
- Ignore generated folders: `"**/dist/**"`, backups: `"**/*.bak"`.
- Ignore known example or placeholder links: `"*example.com*"`, `"https://example.com/foo"`.

