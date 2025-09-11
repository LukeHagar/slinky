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
        with:
          path: .
          patterns: "**/*"
          md-out: results.md
          json-out: results.json
          comment-pr: "true"
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
slinky check . --patterns "docs/**/*.md" --patterns "**/*.go" --md-out results.md --json-out results.json
```

TUI mode:

```bash
slinky run . --patterns "**/*"
```

### Notes

- Respects `.gitignore`.
- Skips likely binary files and files > 2 MiB.
- Uses a browser-like User-Agent to reduce false negatives.

