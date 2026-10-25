# Uptime Kuma

- **Upgrade**: change the image in `compose.yaml`, then `docker compose up -d`.
  The database is migrated on start; a major upgrade (1.x to 2.x) can take a
  while on a large history.
- **New domain**: set the new URL in Settings → General → Primary Base URL, so
  links in notifications point to it.
