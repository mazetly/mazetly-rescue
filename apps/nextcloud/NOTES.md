# Nextcloud

- **Consistency**: the backup was taken in maintenance mode, so the database and
  `volumes/data/` match. The post-restore commands turn maintenance mode off and
  tell sync clients the server was restored (`maintenance:data-fingerprint`).
- **Secrets**: `instanceid`, `passwordsalt` and `secret` live in
  `volumes/config/config.php`; keep that file. If server-side encryption was on,
  its keys are under `volumes/data/` and are restored with it.
- **Upgrade**: one major version at a time. Change the `app` image in
  `compose.yaml`, then `docker compose up -d`: the image upgrades the code and
  runs `occ upgrade` on start.
- **New domain**: update `trusted_domains` and `overwrite.cli.url` with
  `docker compose exec -u www-data app php occ config:system:set`.
- **Background jobs**: re-create the cron job running
  `docker compose exec -T -u www-data app php -f cron.php` every 5 minutes.
