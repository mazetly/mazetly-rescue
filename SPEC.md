# Restore kit format — v0 (draft)

*English · [Français](SPEC.fr.md)*

This document describes what a Mazetly backup contains and how to bring it back
into service without Mazetly. It is the reference: the `mazetly-rescue` tool is
only a convenient way to follow this procedure, not a requirement for following it.

## Principles

1. **The kit is self-sufficient.** With a server, Docker, restic and jq, a competent
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
- `mazetly-rescue copy --to <repo> --init` creates the copy with the source's
  chunking parameters (so copies deduplicate) and the customer key; later runs
  of `copy --to <repo>` send the snapshots it lacks.
- *Open problem*: restic reads S3 credentials from environment variables shared
  by both repositories, so a direct copy only works when one access key opens
  both buckets. Two providers with two keys need rclone remotes, one per
  bucket. To be solved before offering the copy.

## Snapshot contents

An instance's directory is its kit. Each restic snapshot contains it whole:

```
kit/
├── compose.yaml   the recipe: services, mounts and x-mazetly fields,
│                  images pinned by digest
├── .env           settings and secrets
├── db/            one export per database (<service>.sql or
│                  <service>.sqlite)
├── volumes/       what a user can change, mounted from here
├── manifest.json  what the backup produced (see below)
├── RESTORE.md     the generic procedure
└── NOTES.md       what is specific to this application
```

`.env` holds secrets in clear text **inside** the snapshot: restic encrypts the
whole, so there is no second encryption layer to manage. Live database files
under `volumes/` (SQLite and its `-wal`/`-shm`) are excluded: `db/` holds their
consistent copy. Exports are not compressed: restic compresses and deduplicates,
and a plain SQL dump that barely changed from one day to the next costs almost
nothing to store again, where a compressed one would be new data every time.

**Secrets outside the repository.** `.env` is made `0600` at every backup and
restore. Database exports are written `0600` in a `0700` directory, exist only
for the time of the backup, and are removed once restic has them, even when the
backup fails. A restore removes them once imported. The repository password
reaches restic through the environment, never the command line.

## Trust boundary

Whoever can write to the repository writes the kit: Mazetly, the customer, or
an attacker holding one of their keys. restic authenticates every snapshot with
the repository's key, so the storage provider alone cannot forge one; a key
holder can. A restore therefore treats the kit as untrusted until accepted:

1. **Nothing runs before the review.** The kit is extracted, its exports are
   checked against the manifest and its images pinned, and only
   `docker compose config` reads it, which runs nothing.
2. **The kit stays within its directory.** `restore` and `verify` refuse a kit
   with a privileged container, a host mount outside the kit (the Docker socket
   included), the host's network, PID, IPC or user namespace, added
   capabilities, host devices, a disabled security profile, an image built
   instead of pulled, or a named volume, secret or config bound to a host path.
   Hooks run inside the kit's containers, never on the host, so they stay within
   the same boundary. `--allow-unsafe` lifts the refusal, knowingly; none of the
   recipes here needs it.
3. **A person accepts it.** `restore` shows the application, its version, each
   service's pinned image and published ports, and the commands it will run,
   then asks before starting. Without a terminal it stops unless given `--yes`.

What the boundary does not cover: the images themselves. They are pinned by
digest to what ran at backup time, from their public registries; a kit cannot
swap one without the digest changing, which the review shows.

## Checking backups

- **`check`** proves, with the key given, that the repository opens and that
  the latest backup's database exports read back with the checksums recorded at
  backup. Fast and harmless; `--max-age 26h` turns it into a monitoring probe.
- **`verify`** proves the application comes back: it restores the latest
  backup into a throwaway directory, publishes no port and keeps off fixed
  container names (so it runs next to the live instance), waits for the
  healthchecks, checks that the running version is the backed-up one, then
  removes everything. A restore only exists once it has been tested: `verify`
  is the test, for Mazetly every night and for a customer whenever they want.

## Recipes: `x-mazetly` in `compose.yaml`

The tool knows no application. A recipe is the application's `compose.yaml`,
in [`apps/<app>/`](apps), with `x-mazetly` fields where Compose has no word for
what a backup needs. Docker ignores them: the file runs as is with
`docker compose up`.

- **The code comes from the image, never from the backup.** Only what a user
  can change is bind-mounted, from `./volumes/`. Everything there is backed up;
  named volumes are not, so databases go through an export instead.
- **`x-mazetly.database`** on a database service: `engine` is `mariadb`,
  `mysql`, `postgres` or `sqlite`. The tool knows how to export and import each
  one; credentials come from the image's usual variables (`MARIADB_*`, `MYSQL_*`,
  `POSTGRES_*`). For `sqlite`, `path` is the file inside the container; it is
  copied with SQLite's online backup API, consistent while the application
  writes.
- **`x-mazetly.hooks`** on any service: commands run in that service, as a
  string or `{user, run}`, at `pre_backup`, `post_backup`, `post_restore` and
  `post_upgrade`.
- **`x-mazetly.upgrade.renew_anon_volumes`**: the image copies its code into an
  anonymous volume once only, so a new image needs
  `docker compose up --renew-anon-volumes`.
- **`$` in commands**: Compose interpolates `x-*` fields too, so a recipe
  writes `$$` for a literal `$`, and `docker compose config` prints it still
  escaped: the tool turns `$$` back into `$` before running a command.
- **`x-mazetly` at the top level**: `app`, the application's name, and
  `version`, how to read the running version: `{service, run}` (a command's
  output) or `{service, label}` (a label of the image).
- **Healthchecks**: every service that takes time to be ready declares a
  Compose `healthcheck`, so `docker compose up --wait` returns only once the
  application answers.

What no field can say (adjusting for a new domain, an application's quirks)
goes in plain text in `NOTES.md`.

Supporting a new application means writing its `compose.yaml`, its `NOTES.md`
and its round-trip test (`apps/<app>/test.sh`): what to create, what to find
again. The test runner, [`test/roundtrip.sh`](test/roundtrip.sh), is the same for
every application and reads nothing but `x-mazetly`.

## `manifest.json`

What the backup produced; everything else is in `compose.yaml`.

```json
{
  "format": "mazetly-kit/0",
  "app": "matomo",
  "app_version": "5.14.2",
  "created_at": "2026-10-25T03:00:00Z",
  "images": {
    "app": "matomo@sha256:…",
    "db": "mariadb@sha256:…"
  },
  "databases": [
    { "service": "db", "engine": "mariadb", "file": "db/db.sql", "sha256": "…" }
  ]
}
```

Images are pinned by digest: you restore **exactly** the backed-up version, then
upgrade if you wish, never the other way round.

## Applications

Each one is backed up on an older release, restored on a fresh machine with the
customer key only, checked, then upgraded, on every change.

| Application | Database | What the test proves | Upgrade tested |
|---|---|---|---|
| [Matomo](apps/matomo) | MariaDB | site, visits, a third-party plugin, a custom logo | 5.13 → 5.14 |
| [GateCHA](apps/gatecha) | SQLite | admin login, an API key, its HMAC signature unchanged | 0.12 → 0.13 |
| [Uptime Kuma](apps/uptime-kuma) | SQLite | admin login, a monitor | 2.0.0 → 2.x |
| [Healthchecks](apps/healthchecks) | PostgreSQL | user, a check, its ping URL and history | v4.4 → v5.0 |
| [Activepieces](apps/activepieces) | PostgreSQL | sign-in, a flow, a variable encrypted with `AP_ENCRYPTION_KEY` | 0.92.2 → 0.92.3 |
| [Nextcloud](apps/nextcloud) | MariaDB | account, a file over WebDAV, backup in maintenance mode | 32 → 33 |
| [Vaultwarden](apps/vaultwarden) | SQLite | invited account, a password and an attachment decrypted with the master password | 1.36 → 1.37 |

What they taught the format:

- **Matomo**: the Marketplace installs plugins among Matomo's own code, so
  third-party plugins get their own directory (`MATOMO_PLUGIN_DIRS`); the image
  needs `--renew-anon-volumes` to upgrade.
- **GateCHA, Uptime Kuma**: a live SQLite file must not be copied as is; it is
  excluded and replaced by a consistent copy.
- **Activepieces**: an encryption key in `.env` and the data it encrypts must
  come back together; the test reveals an encrypted value to prove it.
- **Nextcloud**: hooks run around the backup (maintenance mode); after an
  upgrade, `post_restore` must not run (it would make every sync client resync),
  hence `post_upgrade`.
- **Vaultwarden**: the database is not enough: attachments, sends and the
  token signing key are files beside it in the data folder, backed up with it.
  The test encrypts like a Bitwarden client and decrypts after the restore, so
  the vault is proven readable, not just present.

## Target environment

Blank Debian 13, root access, at least 2 GB of RAM (4 GB for Nextcloud).
The tool needs Docker, nothing else: restic and SQLite run from their official
images, pinned by digest.

## Out of scope for the tool

- No DNS or certificates: it shows what needs to be configured.
- No version migration: it restores as is.
- No partial restore in v1.

## Decisions

- **Backup: restic**, for its multiple keys and documented format.
- **Recipes: `x-mazetly` fields in `compose.yaml`**, not a separate file that
  would have to be kept in step with it.
- **Tool: Go binary**, a single file for Linux amd64 and arm64, with two
  dependencies (`gopkg.in/yaml.v3`, `golang.org/x/term`). restic is not a library
  (its code lives in `internal/`): the tool runs the official restic image,
  pinned by digest, so a server needs Docker and nothing else.
  `docker compose config --format json` drops `x-*` fields; the tool reads the
  YAML output.
- **Commands**: `check` and `verify` (see "Checking backups"), `restore` (the
  steps of `RESTORE.md`, behind the trust boundary), `backup` and `init`
  (Mazetly's side), `copy` (to the customer's bucket).
- **Languages: English and French.** English is the reference for the
  specification (`SPEC.md`); French is its translation (`SPEC.fr.md`). The tool
  speaks the system language (`LANG`), English by default; `RESTORE.md` is written
  in the customer's language.
- **Public proof**: the [Round trip](.github/workflows/roundtrip.yml) CI backs
  every application up with `mazetly-rescue backup`, then restores it twice on
  fresh machines with the customer key only: by hand following `RESTORE.md`
  (then upgrades it), and with `mazetly-rescue restore`. On every change and
  every week.
