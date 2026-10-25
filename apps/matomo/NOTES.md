# Matomo

- **Upgrade**: change the `app` image in `compose.yaml`, then run
  `docker compose up -d --renew-anon-volumes app` and
  `docker compose exec -T -u www-data app php console core:update --yes`.
  Without `--renew-anon-volumes`, the old code keeps running.
- **Third-party plugins** live in `volumes/plugins/`. A plugin installed from the
  Marketplace in Matomo's interface lands among Matomo's own code and is not kept.
- **New domain**: update `trusted_hosts[]` in `volumes/config/config.ini.php`.
- **Archiving**: re-create the cron job running
  `docker compose exec -T -u www-data app php console core:archive`.
