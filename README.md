# rdb — restic-docker-backup

Automated [restic](https://restic.net) backups for Docker Compose environments. Discovers containers via Docker labels and backs up named volumes and database dumps (PostgreSQL, MySQL, MariaDB) on a cron schedule.

## Quick start

```yaml
# docker-compose.yml
services:
  rdb:
    image: ghcr.io/scootec/rdb:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
    environment:
      RESTIC_REPOSITORY: s3:s3.amazonaws.com/my-bucket/backups
      RESTIC_PASSWORD: a-strong-password
      AWS_ACCESS_KEY_ID: ...
      AWS_SECRET_ACCESS_KEY: ...

  postgres:
    image: postgres:17
    labels:
      rdb.postgres: "true"
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret

  app:
    image: myapp
    labels:
      rdb.volumes: "true"
    volumes:
      - app-data:/data

volumes:
  app-data:
```

rdb initialises the repository on first run and then backs up on the configured schedule (default: 02:00 daily).

## Container labels

| Label | Values | Description |
|---|---|---|
| `rdb.volumes` | `true` | Back up this container's named volumes |
| `rdb.volumes.include` | comma-separated paths | Only back up these mount destinations |
| `rdb.volumes.exclude` | comma-separated paths | Skip these mount destinations |
| `rdb.volumes.stop-during-backup` | `true` | Stop the container while backing up (for crash-consistency) |
| `rdb.postgres` | `true` | Dump all PostgreSQL databases (`pg_dumpall`) |
| `rdb.mysql` | `true` | Dump all MySQL databases (`mysqldump --all-databases`) |
| `rdb.mariadb` | `true` | Dump all MariaDB databases (`mariadb-dump --all-databases`) |

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `RESTIC_REPOSITORY` | **required** | Restic repository URL |
| `RESTIC_PASSWORD` | **required** | Repository encryption password |
| `RDB_CRON_SCHEDULE` | `0 2 * * *` | Backup schedule (5-field cron) |
| `RDB_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `RDB_EXCLUDE_BIND_MOUNTS` | `false` | Skip host bind mounts during volume backup |
| `RDB_SKIP_INIT` | `false` | Skip automatic repository initialisation |
| `RESTIC_KEEP_DAILY` | `7` | Daily snapshots to keep |
| `RESTIC_KEEP_WEEKLY` | `4` | Weekly snapshots to keep |
| `RESTIC_KEEP_MONTHLY` | `12` | Monthly snapshots to keep |
| `RESTIC_KEEP_YEARLY` | `3` | Yearly snapshots to keep |
| `RESTIC_KEEP_LAST` | `0` (off) | Keep the last N snapshots regardless of date |
| `RESTIC_KEEP_HOURLY` | `0` (off) | Hourly snapshots to keep |
| `RESTIC_KEEP_WITHIN` | `` (off) | Keep all snapshots within a duration (e.g. `2w3d`, `1y`) |

All backend credentials recognised by restic (`AWS_*`, `B2_*`, `AZURE_*`, `GOOGLE_*`, etc.) are passed through automatically.

## Database credentials

rdb reads credentials from the target container's environment variables — no separate configuration needed.

| Database | Variables read |
|---|---|
| PostgreSQL | `POSTGRES_USER`, `POSTGRES_PASSWORD` |
| MySQL | `MYSQL_ROOT_PASSWORD` (preferred), or `MYSQL_USER` + `MYSQL_PASSWORD` |
| MariaDB | `MARIADB_ROOT_PASSWORD` (preferred), or `MARIADB_USER` + `MARIADB_PASSWORD` |

Dumps are stored in restic as `/databases/<project>/<service>/all_databases.sql`.

## Volume backup

Named volumes are accessed via `/var/lib/docker/volumes` mounted read-only into the rdb container. The rdb container must have this mount:

```yaml
volumes:
  - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
```

Volumes are stored in restic under their host path. Bind mounts are included by default; set `RDB_EXCLUDE_BIND_MOUNTS=true` to skip them.

## CLI commands

```
rdb run          Start the cron scheduler (default container entrypoint)
rdb backup       Run a backup immediately
rdb status       Show discovered containers and their backup config
rdb snapshots    List restic snapshots
rdb maintenance  Run forget + prune + check
```

## Supported repositories

Any restic backend works: local path, SFTP, S3, Backblaze B2, Azure, Google Cloud Storage, rclone, and more. See the [restic documentation](https://restic.readthedocs.io/en/latest/030_preparing_a_new_repo.html) for backend-specific setup.

## Releasing

Releases are fully automated via [semantic-release](https://semantic-release.gitbook.io). Push commits to `main` using [Conventional Commits](https://www.conventionalcommits.org) and the pipeline handles everything else.

| Commit type | Version bump |
|-------------|--------------|
| `feat:` | minor — e.g. `1.2.0 → 1.3.0` |
| `fix:`, `perf:` | patch — e.g. `1.2.0 → 1.2.1` |
| `feat!:` or `BREAKING CHANGE:` footer | major — e.g. `1.2.0 → 2.0.0` |
| `docs:`, `chore:`, `refactor:`, `ci:`, `test:` | no release |

When a release is warranted, the pipeline automatically:

1. Determines the next version from commit history
2. Creates and pushes a `vX.Y.Z` git tag
3. Publishes a GitHub Release with auto-generated notes
4. Builds multi-platform Docker images (`linux/amd64`, `linux/arm64`) and pushes them to GHCR

The following GHCR image tags are produced on each release:

| Tag | Example |
|-----|---------|
| Full version | `ghcr.io/scootec/rdb:1.2.3` |
| Major.minor | `ghcr.io/scootec/rdb:1.2` |
| `latest` | `ghcr.io/scootec/rdb:latest` |

No manual tagging is needed. See [CLAUDE.md](CLAUDE.md) for commit message guidelines.

## Building from source

```sh
go build -o rdb ./cmd/rdb
```

Requires Go 1.24+ and a `restic` binary on `PATH`.
