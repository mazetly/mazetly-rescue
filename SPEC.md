# Restore kit format — v0 (draft)

*English · [Français](SPEC.fr.md)*

This document describes what a Mazetly backup contains and how to bring it back
into service without Mazetly. It is the reference: the `mazetly-rescue` tool is
only a convenient way to follow this procedure, not a requirement for following it.

## Principles

1. **The kit is self-sufficient.** With a server, Docker and restic, a competent
   person must be able to restore the application by reading `RESTORE.md`, without
   the tool, without an account, without any Mazetly API.
2. **The kit is the backup.** Mazetly has no other internal format. Every restore
   Mazetly performs goes through this format and this tool.
3. **A restore only exists once it has been tested.** A kit is declared valid only
   after a successful automated restore on a blank machine.
4. **No proprietary format.** restic, Docker Compose, standard SQL dumps, JSON.

## Storage: restic

Each application instance has its own [restic](https://restic.net)
(BSD 2-Clause) repository on S3 (`s3:<endpoint>/<bucket>/<instance>`).

- restic encrypts everything (AES-256 + Poly1305), compresses, deduplicates and
  writes natively to S3. Its repository format is
  [publicly documented](https://restic.readthedocs.io/en/stable/100_references.html),
  and an independent second implementation exists ([rustic](https://rustic.cli.rs)):
  a backup stays readable even without restic.

### Keys: one per party, never shared

Data is encrypted with a single master key, randomly generated when the repository
is created. Each password is a separate wrapping of it (the repository's `keys/`):

- **Mazetly key**: used for backups. It is never disclosed to the customer.
- **Customer key**: created at onboarding (`restic key add`) and handed to the
  customer. Mazetly keeps no copy. The customer can change it at any time
  (`restic key passwd`) without telling Mazetly or interrupting backups.

Either key opens the repository on its own. restic has no roles: any key can list,
add and remove the others. **S3 permissions are what decide**:

| Bucket | Mazetly | Customer |
|---|---|---|
| Mazetly storage | read and write | read-only, scoped to their instance: they can restore (`--no-lock`) but cannot touch any key |
| Customer bucket | write, through the S3 key the customer entrusted to Mazetly | owner |

In the customer bucket, Mazetly could technically remove the customer's key.
Versioning makes it recoverable for 30 days, and `mazetly-rescue check` verifies
that the customer's key still opens the repository.

Removing a key (`restic key remove`) does not erase what a former holder may have
kept, since the master key does not change: to really change the encryption key,
a new repository must be created.

### Copy to the customer's bucket (optional)

Mazetly copies snapshots into a second restic repository, in an S3 bucket provided
by the customer (`restic copy`). That repository has its own master key and the
same two kinds of key: Mazetly's to write, the customer's to restore.

- The customer creates the bucket and an S3 access key scoped to it. **This is
  their real lever**: by revoking that key at their provider, they cut off all
  Mazetly access to their copy.
- **Bucket versioning is mandatory**, with non-current versions expiring after
  30 days: a deletion made by mistake or by an attacker coming through Mazetly
  stays recoverable for 30 days. Object Lock on top when the provider offers it.
  *To be validated: `forget`/`prune` on a locked bucket.*
- Storage is billed to the customer by their provider. Mazetly charges nothing
  extra.

## Snapshot contents

Each restic snapshot contains a single `kit/` directory:

```
kit/
├── manifest.json        machine-readable description of the kit (see below)
├── RESTORE.md           human procedure, specific to this application
├── compose.yaml         services, images pinned by digest
├── .env                 instance variables and secrets
├── db/
│   └── <service>.sql.zst  logical dump of each database
└── volumes/
    └── <name>/          contents of each persistent volume
```

`.env` holds secrets in clear text **inside** the snapshot: restic encrypts the
whole, so there is no second encryption layer to manage.

## `manifest.json`

```json
{
  "format": "mazetly-kit/0",
  "app": "matomo",
  "app_version": "5.x.y",
  "created_at": "2026-10-25T03:00:00Z",
  "instance": "acme-matomo",
  "domain": "stats.acme.fr",
  "images": {
    "app": "matomo@sha256:…",
    "db": "mariadb@sha256:…"
  },
  "databases": [
    { "service": "db", "engine": "mariadb", "file": "db/db.sql.zst", "sha256": "…" }
  ],
  "volumes": [
    { "path": "volumes/config/config.ini.php", "mount": "/var/www/html/config/config.ini.php" },
    { "path": "volumes/plugins", "mount": "/var/www/html/extra-plugins" },
    { "path": "volumes/misc-user", "mount": "/var/www/html/misc/user" }
  ],
  "post_restore": [
    "docker compose exec -T app ./console core:update --yes"
  ],
  "consistency": "mysqldump --single-transaction"
}
```

Images are pinned by digest: you restore **exactly** the backed-up version, then
upgrade if you wish, never the other way round.

## Recipes

The tool knows no application. What is specific to one lives in a recipe,
`recipes/<app>.yaml`:

- **services and images**: which image, which version track;
- **databases**: engine (`mariadb`, `mysql`, `postgres`, `sqlite`) and where its
  credentials are; the tool knows how to dump and import each engine;
- **volumes**: only the paths a user can change; the code always comes from the
  image, never from the backup;
- **hooks**: commands to run before or after a backup or a restore (maintenance
  mode, migrations);
- **upgrade**: what a new image needs to take effect;
- **domain change**: what to adjust when the instance moves to another domain;
- **checks**: what proves the restored instance works.

When a kit is produced, the recipe is instantiated into `manifest.json` and
`RESTORE.md`: a kit never depends on the recipe that produced it. Supporting a new
application means writing its recipe and its round-trip test, not changing the
tool.

### Matomo (v1)

[`recipes/matomo.yaml`](recipes/matomo.yaml), proven by
[`test/matomo/roundtrip.sh`](test/matomo/roundtrip.sh).

- **Backed up**: database dump (`mysqldump --single-transaction`, consistent
  without downtime), `config/config.ini.php` (mounted as a single file),
  third-party plugins, `misc/user/`. Everything else is Matomo's code and comes
  from the image: a kit weighs about as much as its dump.
- **Third-party plugins**: the Marketplace always installs into `plugins/`, among
  the bundled plugins, where the image's code lives. Third-party plugins are
  therefore placed in their own directory, loaded through `MATOMO_PLUGIN_DIRS`;
  Marketplace installs from the Matomo interface are not kept.
- **Upgrade**: the image copies its code into an anonymous volume once only;
  a new image needs `docker compose up --renew-anon-volumes`, then
  `console core:update`.
- **To adjust** if the domain changes: `trusted_hosts[]` in `config.ini.php`.

### Nextcloud (v2)

- **Backup**: `occ maintenance:mode --on`, database dump, `data/` directory
  (including server-side encryption keys if enabled), `config/config.php`
  (`instanceid`, `passwordsalt`, `secret`), `custom_apps/`, then
  `occ maintenance:mode --off`. The maintenance window lasts as long as the dump;
  `data/` is copied afterwards, with the known risk of files changing during the
  copy, which `files:scan` fixes.
- **Restore**: same version, import, `occ maintenance:data-fingerprint`,
  `occ files:scan --all`, adjust `trusted_domains` and `overwrite.cli.url`,
  reconfigure Redis and cron.
- **Size**: `data/` can weigh hundreds of GB; the first copy to the customer's
  bucket has to be scheduled.

## Target environment

Blank Debian 13, root access, at least 2 GB of RAM (4 GB for Nextcloud).
The tool installs Docker and restic from their official sources if needed.

## Out of scope for the tool

- No DNS or certificates: it shows what needs to be configured.
- No version migration: it restores as is.
- No partial restore in v1.

## Decisions

- **Backup: restic**, for its multiple keys and documented format.
- **Tool: Go binary**, a single file for Linux amd64 and arm64. restic is not a
  library (its code lives in `internal/`): the tool calls the official restic
  binary, downloading and verifying it (SHA-256 checksum and signature) if missing.
- **Languages: English and French.** English is the reference for the
  specification (`SPEC.md`); French is its translation (`SPEC.fr.md`). The tool
  speaks the system language (`LANG`), English by default; `RESTORE.md` is written
  in the customer's language.

- **Public proof**: the [Round trip](.github/workflows/roundtrip.yml) CI installs
  a reference Matomo, produces its kit and backs it up, then restores it on a fresh
  machine with the customer key only, by following the kit's `RESTORE.md`. It runs
  on every change and every week (`test/matomo/roundtrip.sh`).
