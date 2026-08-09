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
rdb restore      Restore a snapshot (see below)
rdb maintenance  Run forget + prune + check
```

## Restoring from backup

### Requirements

For restore to work, the rdb container needs **write access** to the paths it will restore to. If you only need backups, read-only mounts are sufficient — but for restore you must mount the data directories read-write:

```yaml
services:
  rdb:
    image: ghcr.io/scootec/rdb:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/lib/docker/volumes:/var/lib/docker/volumes      # read-write for restore
      - /home/user/containers:/home/user/containers           # read-write for restore
```

Volume restore writes files back to their original absolute paths inside the rdb container, restricted to the paths recorded in the snapshot (`restic restore --include`), and verifies the restored files against the repository (`--verify`). If the host path isn't mounted (or is mounted read-only), the restore will fail.

### Listing snapshots

Use `rdb snapshots` to list all snapshots with their IDs, types, and metadata:

```
ID        Time              Type       Project    Service    Path
abc123    2026-03-28 02:00  postgres   myapp      db         /databases/myapp/db/postgres/all_databases.sql
def456    2026-03-28 02:00  volume     myapp      app        /var/lib/docker/volumes/myapp_app-data/_data
```

### Recommended restore order

1. Start only the database containers and rdb:
   ```sh
   docker compose up -d rdb postgres
   ```

2. Restore the database (the DB container must be running to accept the import):
   ```sh
   docker exec rdb rdb restore <db-snapshot-id>
   ```
   Make sure no applications are connected to the database during the restore — PostgreSQL refuses to drop a database with active connections, which aborts the restore.

3. Restore volumes (with app containers stopped so nothing writes to the data directories):
   ```sh
   docker exec rdb rdb restore <volume-snapshot-id>
   ```

4. Start the rest of the stack:
   ```sh
   docker compose up -d
   ```

rdb automatically detects the snapshot type (volume, PostgreSQL, MySQL, MariaDB) from tags and locates the target container by its Compose project and service labels.

### What a database restore does

Database dumps are created with clean/drop statements (`pg_dumpall --clean --if-exists` for PostgreSQL, `--add-drop-table` for MySQL/MariaDB), so a restore does not require an empty server:

- Every database, role, and table **present in the backup** is dropped and recreated, so those objects end up exactly in the backup state — no "already exists" or duplicate-key errors.
- Objects created **after** the backup was taken (extra databases, roles, or tables that are not in the backup) are **not** removed. If you need a target that exactly matches the backup, restore into a fresh database container.
- For PostgreSQL, statements affecting the role the restore connects as (`POSTGRES_USER`) are skipped — that role always already exists in the target, and PostgreSQL would reject dropping it. Its attributes are still applied via `ALTER ROLE`.

The import fails loudly on the first unexpected SQL error instead of continuing with partial data.

Snapshots taken with older rdb versions (before clean/drop statements were added) still restore into a **fresh** database container, but restoring them into a non-empty server will fail on the first "already exists" error.

### Volume restore safety

Before restoring a volume, rdb checks whether the container that owns the volume is still running (located via the snapshot's project/service tags). Restoring under a running application or database corrupts data, so a restore against a running container is refused unless you pass one of:

```sh
# Stop the container, restore, then restart it
docker exec rdb rdb restore <volume-snapshot-id> --stop

# Restore anyway while the container keeps running (dangerous)
docker exec rdb rdb restore <volume-snapshot-id> --force
```

If the owning container simply isn't running (the normal disaster-recovery case), the restore proceeds without any flags. `--force` also bypasses failures to determine the container's state (for example when multiple containers match the snapshot's tags).

### Dump to file

Extract a database SQL dump to a file instead of importing it:

```sh
docker exec rdb rdb restore <snapshot-id> --output /tmp/dump.sql
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
