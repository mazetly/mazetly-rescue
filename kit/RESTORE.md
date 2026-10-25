# Restoring this application

`mazetly-rescue restore` does everything below in one command
(<https://github.com/mazetly/mazetly-rescue>). This page is the same procedure
by hand, for when you would rather not run our tool: it only needs Docker,
restic and jq.

This kit holds everything needed to run the application again on any Linux
server: `compose.yaml` (the services, and in `x-mazetly` what a restore needs to
know), `.env` (settings and secrets), `db/` (database exports), `volumes/` (the
files a user could change) and `manifest.json` (what the backup recorded).
`NOTES.md` holds what is specific to this application.

1. **Restore the kit**, as root so file owners are kept:

   ```sh
   export RESTIC_REPOSITORY=<repository> RESTIC_PASSWORD=<your restore key>
   restic snapshots --tag mazetly-kit          # note the ID and the path
   mkdir /srv/app
   restic restore <ID>:<path> --target /srv/app
   cd /srv/app
   ```

2. **Check the database exports** against the manifest:

   ```sh
   jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c
   ```

3. **Pin the images** to the exact versions that were backed up: in
   `compose.yaml`, set each service's `image:` to its value in `manifest.json`.

   ```sh
   jq -r '.images | to_entries[] | "\(.key): \(.value)"' manifest.json
   ```

4. **Restore each database** listed in `manifest.json`:

   - `sqlite`, before starting anything: copy `db/<service>.sqlite` to the
     database `path` from the manifest, under the matching directory of
     `volumes/`, and give it the owner of that directory.
   - `mariadb`, `mysql`, `postgres`: start the database service with
     `docker compose up -d --wait <service>`, then import its export:

   | Engine | Command |
   |---|---|
   | `mariadb` | `docker compose exec -T <service> sh -c 'exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"' < db/<service>.sql` |
   | `mysql` | `docker compose exec -T <service> sh -c 'exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"' < db/<service>.sql` |
   | `postgres` | `docker compose exec -T <service> sh -c 'exec psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"' < db/<service>.sql` |

5. **Start everything**:

   ```sh
   docker compose up -d --wait
   ```

6. **Run the post-restore commands** listed under `x-mazetly.hooks.post_restore`
   in `compose.yaml`, in the service they belong to:
   `docker compose exec -T [-u <user>] <service> sh -c '<command>'`.

7. **Read `NOTES.md`**: what to adjust for a new domain, how to upgrade.
   Then point your domain at the server and put a TLS proxy in front of the
   published port.
