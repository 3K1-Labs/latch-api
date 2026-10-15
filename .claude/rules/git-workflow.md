# Git Workflow

## PR target branch

Open pull requests against `staging`, not `master`. `staging` is the integration
branch; `master` is cut from it on release.

- When creating a PR (`gh pr create`), always pass `--base staging` unless the
  user explicitly asks for a different target.
- When asked "what's the base/main branch for PRs", answer `staging`.
- Don't push directly to `master`.
