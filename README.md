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

| Application | Status |
|---|---|
| Matomo | kit format tested in CI; tool planned (v1) |
| Nextcloud | planned (v2) |

## License

[Apache 2.0](LICENSE).
