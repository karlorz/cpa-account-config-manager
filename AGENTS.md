# AGENTS.md

Standalone CLIProxyAPI native plugin for safe account configuration listing and batch edits.

## Repository identity

This is a fork (`karlorz/cpa-account-config-manager`) of the upstream project
`Mxucc/cpa-account-config-manager`. `origin` is the fork; `upstream` is the
original. Never push branches or tags to `upstream`.

## Tag policy (mandatory)

Follow the same fork-series convention as `karlorz/cpa-glm-vision-bridge`:

- **Fork release tags always use the `vX.Y.Z-N` series**: `v0.3.1332-0`,
  `v0.3.1332-1`, ... (`N` is a non-negative integer, incremented per fork
  release of the same upstream version). The release version string is the
  tag without the leading `v`.
- **Never create, replace, force-update, or delete tags owned by upstream.**
  Upstream owns the plain `vX.Y.Z` names (`v0.3.1332`, `v0.3.1333`, and so
  on). In this repo those tags must only ever point at upstream's commits,
  created by `git fetch upstream --tags`. Do not invent the next upstream
  patch (`v0.3.1333`) for a fork release.
- If a local tag name collides with an upstream tag, the local one is a
  policy violation: rename or delete it (e.g. `git tag -d v0.3.1333`), then
  re-fetch upstream tags. If the offending tag was pushed, also delete it
  from origin (`git push origin :refs/tags/<name>`).
- Before a fork release: bump `registry.json` `version` to the fork series
  name, confirm HEAD is the intended commit, then tag and push
  `v<same-version>` to **origin only**. The `check` job in
  `.github/workflows/release.yml` enforces both rules (pattern + upstream
  collision) and gates the build.

## Upstream sync

`git fetch upstream --tags` is safe and expected. Sync upstream changes with
`git merge upstream/main`. Keep fork-only files (`AGENTS.md` tag policy,
`.github/workflows/release.yml` fork-tag check, `DefaultPluginRepository`,
`registry.json`) while taking upstream's code changes.

## Commands

```bash
gofmt -w .
go test ./...
make build
make verify
```

## Conventions

- Keep the CGO ABI bridge thin; business logic belongs under `internal/` and must be testable without loading CLIProxyAPI.
- Treat Management Keys, Auth JSON, tokens, cookies, API keys, proxy credentials, and header values as secrets.
- Never persist or log secrets. Public API models must be explicitly allow-listed and redacted.
- Plugin Management routes are exact paths. Do not use dynamic path parameters.
- Resource routes serve static UI only. Privileged data and writes belong behind authenticated Management routes.
- Comments and new Markdown documentation are English unless a language-specific file is explicitly created.
- Use contextual errors and bounded concurrency. Do not panic in request or job paths.
