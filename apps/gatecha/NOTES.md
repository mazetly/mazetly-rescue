# GateCHA

- **Secrets**: keep `GATECHA_SECRET_KEY` from `.env`. The admin password and the
  HMAC secrets of the API keys are in the database: the sites using this
  instance keep working without any change.
- **Upgrade**: change the image in `compose.yaml`, then `docker compose up -d`.
  The database schema is migrated on start.
- **New domain**: nothing to change in GateCHA; update the sites' widget URL.
  Behind a reverse proxy, set `GATECHA_TRUST_PROXY=true` in `.env`.
