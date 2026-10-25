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
- a blank Debian server.

## Status

Under design. The backup format is described in [SPEC.md](SPEC.md).

Each application's recipe is tested end to end in CI: backed up, restored on a
fresh machine with the customer key only, checked, then upgraded. The tool that
will do this for you is next.

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
