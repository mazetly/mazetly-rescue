# Restoring this Matomo instance

You need a Linux server with Docker, restic, zstd and jq, the restic repository
holding this kit, and your restore key.

1. Restore the kit (as root, so file ownership is kept):

   ```sh
   export RESTIC_REPOSITORY=<repository> RESTIC_PASSWORD=<your restore key>
   restic restore latest --no-lock --target /srv/restore
   cd /srv/restore/kit
   ```

2. Check the database dump against the manifest:

   ```sh
   jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c
   ```

3. Start the database and import the dump:

   ```sh
   docker compose up -d --wait db
   . ./.env
   zstd -dc db/db.sql.zst | docker compose exec -T db \
     mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"
   ```

4. Start Matomo and run the post-restore steps listed in `manifest.json`:

   ```sh
   docker compose up -d app
   jq -r '.post_restore[]' manifest.json | sh
   ```

5. To upgrade later, change the `app` image in `compose.yaml`, then:

   ```sh
   docker compose up -d --renew-anon-volumes app
   docker compose exec -T app php console core:update --yes
   ```

   `--renew-anon-volumes` is required: the image copies Matomo's code into an
   anonymous volume once only, and would otherwise keep running the old code.

6. Point your domain at the server, put a TLS proxy in front of port 8080, and if
   the domain changed, update `trusted_hosts[]` in
   `volumes/config/config.ini.php`. Re-create the archiving cron job:

   ```sh
   docker compose exec -T app php console core:archive
   ```
