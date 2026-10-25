# Healthchecks

- **Secrets**: keep `SECRET_KEY` from `.env`; changing it logs everyone out and
  breaks unsubscribe links already sent.
- **Upgrade**: change the `web` image in `compose.yaml`, then
  `docker compose up -d`. Migrations run on start.
- **New domain**: set `SITE_ROOT` and `ALLOWED_HOSTS` in `.env`. Ping URLs
  contain the domain: every monitored job must be pointed at the new one.
