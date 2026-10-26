# Vaultwarden

- **Secrets**: vaults are encrypted on the users' devices with keys derived from
  their master passwords; neither the server nor this backup can read them, and
  a forgotten master password cannot be recovered from it. `rsa_key.pem` in
  `volumes/data` signs login tokens: without it, everyone is logged out, nothing
  is lost. `ADMIN_TOKEN` in `.env` is an Argon2 hash of the admin page's token,
  made with `docker compose exec vaultwarden /vaultwarden hash`; keep it in
  single quotes, or Compose reads its `$` as variables.
- **Accounts**: sign-up is closed; invite users from `/admin`. Without SMTP
  settings in `.env`, an invited address registers directly; with them, the
  invitation is mailed.
- **Upgrade**: change the image in `compose.yaml`, then `docker compose up -d`.
  The database is migrated on start.
- **New domain**: set `DOMAIN` in `.env` (with `https://`). Every client
  (browser extension, mobile and desktop apps) must be pointed at the new
  server URL; vaults come back as they were after logging in again.
- **Name**: Vaultwarden is an independent server compatible with the Bitwarden
  apps, not affiliated with Bitwarden.
