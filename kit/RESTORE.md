# Restoring this application

This kit holds everything needed to run the application again on any Linux
server: `compose.yaml` (the services, with images pinned to the exact versions
that were backed up), `.env` (settings and secrets), `db/` (database exports),
`volumes/` (the files a user could change) and `manifest.json`. `NOTES.md` holds
what is specific to this application. You need Docker, restic, zstd and jq.

1. **Restore the kit**, as root so file owners are kept. `restic snapshots`
   shows the path that was backed up; restore that path:

   ```sh
   export RESTIC_REPOSITORY=<repository> RESTIC_PASSWORD=<your restore key>
   restic snapshots
   restic restore latest:<path>/kit --no-lock --target /srv/app
   cd /srv/app
   ```

2. **Check the database exports** against the manifest:

   ```sh
   jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c
   ```

3. **Restore each database** listed in `manifest.json`, by engine.
   For MariaDB, MySQL and PostgreSQL, start the database service first with
   `docker compose up -d --wait <service>`, then:

   | Engine | Command |
   |---|---|
   | `mariadb` | `zstd -dc db/<service>.sql.zst \| docker compose exec -T <service> sh -c 'exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"'` |
   | `mysql` | `zstd -dc db/<service>.sql.zst \| docker compose exec -T <service> sh -c 'exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"'` |
   | `postgres` | `zstd -dc db/<service>.sql.zst \| docker compose exec -T <service> sh -c 'exec psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"'` |
   | `sqlite` | before starting anything: `zstd -dc db/<service>.sqlite.zst > <file>`, where `<file>` is the database `path` from the manifest, under the matching directory of `volumes/`; give it the owner of that directory |

4. **Start everything**:

   ```sh
   docker compose up -d --wait
   ```

5. **Run the post-restore commands** listed under `x-mazetly.hooks.post_restore`
   in `compose.yaml`, in the service they belong to (`docker compose exec -T
   [-u <user>] <service> sh -c '<command>'`).

6. **Read `NOTES.md`**: what to adjust for a new domain, how to upgrade.
   Then point your domain at the server and put a TLS proxy in front of the
   published port.
