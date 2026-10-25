# Activepieces

- **Secrets**: `AP_ENCRYPTION_KEY` in `.env` encrypts every connection and
  variable in the database. Without it, they are lost for good: keep it with the
  backup. `AP_JWT_SECRET` signs sessions; changing it only logs everyone out.
- **Upgrade**: change the `app` image in `compose.yaml`, then
  `docker compose up -d`. Migrations run on start.
- **New domain**: set `AP_FRONTEND_URL` in `.env`. Webhook URLs contain the
  domain: every service calling a webhook trigger must be pointed at the new one.
- **License**: this recipe runs the Community edition (MIT). Some features ship
  under Activepieces' commercial license and are not enabled.
