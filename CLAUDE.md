# Claude Agent Instructions for rdb

## Commit Message Convention

This repository uses **Conventional Commits** to drive automatic semantic versioning via
[semantic-release](https://semantic-release.gitbook.io). Every commit pushed to `main` is
analysed, and if it warrants a release, a new version is tagged and a GitHub Release is
created automatically. The Docker image is then built and pushed to GHCR.

**Never manually create or push version tags.** semantic-release manages all tagging.

### Format

```
<type>(<scope>): <short summary>

[optional body]

[optional footer(s)]
```

The `scope` is optional but encouraged. Common scopes for this project:
`backup`, `docker`, `config`, `scheduler`, `restic`, `cli`

### Types and version impact

| Type | Release triggered | Version bump |
|------|-------------------|--------------|
| `feat` | yes | **minor** — `1.2.0 → 1.3.0` |
| `fix` | yes | patch — `1.2.0 → 1.2.1` |
| `perf` | yes | patch — `1.2.0 → 1.2.1` |
| `feat!` or `BREAKING CHANGE:` footer | yes | **major** — `1.2.0 → 2.0.0` |
| `docs` | no | — |
| `chore` | no | — |
| `refactor` | no | — |
| `test` | no | — |
| `ci` | no | — |
| `style` | no | — |
| `build` | no | — |

### Examples

```
feat(backup): add support for rclone repositories

fix(docker): handle containers with no environment variables

perf(restic): stream stdout instead of buffering full output

docs: update README with MariaDB credential variables

chore: upgrade docker SDK to v27.5.1

refactor(config): extract env parsing into separate functions

ci: add semantic-release with automated Docker publish
```

### Breaking changes

Signal a breaking change in one of two ways:

**Option 1** — append `!` to the type:
```
feat!: redesign config env var names
```

**Option 2** — add a `BREAKING CHANGE:` footer in the commit body:
```
feat(config): rename all env vars

BREAKING CHANGE: All RDB_ prefix variables are renamed. See README for new names.
```

Either form triggers a major version bump.

### Rules for agents

- ALWAYS use one of the types listed above.
- NEVER use free-form messages like "update stuff", "wip", or "changes".
- If a commit fixes a bug, use `fix`. If it adds new user-visible behaviour, use `feat`.
- Internal refactors with no observable behaviour change use `refactor` (no release).
- If unsure between `feat` and `fix`: corrections are `fix`, new capabilities are `feat`.
- NEVER manually create or push version tags — semantic-release manages all tagging.
