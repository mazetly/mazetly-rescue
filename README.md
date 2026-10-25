# Mazetly Rescue

*English · [Français](README.fr.md)*

[![Round trip](https://github.com/mazetly/mazetly-rescue/actions/workflows/roundtrip.yml/badge.svg)](https://github.com/mazetly/mazetly-rescue/actions/workflows/roundtrip.yml)

> Don't take our word for it. Check that you can do without us.

[Mazetly](https://mazetly.fr) hosts free software for its customers on OVHcloud
servers in France. Mazetly Rescue lets anyone bring an application hosted by
Mazetly back into service on a blank server, **without Mazetly**: no account, no
API, nothing to ask us.

You need:

- your backup (in Mazetly's storage, or in your own S3 bucket);
- the restore key you were given at onboarding;
- a Linux server with Docker.

## Usage

```sh
export RESTIC_REPOSITORY=s3:https://s3.fr-par.scw.cloud/my-bucket/my-app
export AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…
mazetly-rescue check                      # does my key open my backups?
mazetly-rescue restore --target /srv/app  # bring the application back
```

The key is asked for when not given in `RESTIC_PASSWORD` or
`--password-file`. Without the tool, [`kit/RESTORE.md`](kit/RESTORE.md) is the
same procedure by hand.

## Status

Version 0: the format, described in [SPEC.md](SPEC.md), and the tool may still
change. No release yet; build the tool with
`go build -o mazetly-rescue ./cmd/mazetly-rescue`.

Each application's recipe is tested end to end in CI: backed up, restored on a
fresh machine with the customer key only (by hand and with the tool), checked,
then upgraded.

| Application | Database | Recipe |
|---|---|---|
| Matomo | MariaDB | [apps/matomo](apps/matomo) |
| GateCHA | SQLite | [apps/gatecha](apps/gatecha) |
| Uptime Kuma | SQLite | [apps/uptime-kuma](apps/uptime-kuma) |
| Healthchecks | PostgreSQL | [apps/healthchecks](apps/healthchecks) |
| Activepieces | PostgreSQL | [apps/activepieces](apps/activepieces) |
| Nextcloud | MariaDB | [apps/nextcloud](apps/nextcloud) |

## License

[Apache 2.0](LICENSE).
