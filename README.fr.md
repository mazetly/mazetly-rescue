# Mazetly Rescue

*[English](README.md) · Français*

[![Round trip](https://github.com/mazetly/mazetly-rescue/actions/workflows/roundtrip.yml/badge.svg)](https://github.com/mazetly/mazetly-rescue/actions/workflows/roundtrip.yml)

> Ne nous faites pas confiance sur parole. Vérifiez que vous pouvez vous passer de nous.

[Mazetly](https://mazetly.fr) héberge des logiciels libres pour ses clients, sur des
serveurs OVHcloud en France. Mazetly Rescue permet à n'importe qui de remettre en
service une application hébergée chez Mazetly sur un serveur vierge, **sans
Mazetly** : sans compte, sans API, sans nous demander quoi que ce soit.

Il vous faut :

- votre sauvegarde (dans le stockage de Mazetly, ou dans votre propre bucket S3) ;
- la clé de restauration qui vous a été remise à la mise en service ;
- un serveur Linux avec Docker.

## Utilisation

```sh
export RESTIC_REPOSITORY=s3:https://s3.fr-par.scw.cloud/mon-bucket/mon-app
export AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…
mazetly-rescue check                      # ma clé ouvre-t-elle mes sauvegardes ?
mazetly-rescue restore --target /srv/app  # remettre l'application en service
```

La clé est demandée si elle n'est pas fournie par `RESTIC_PASSWORD` ou
`--password-file`. Sans l'outil, [`kit/RESTORE.md`](kit/RESTORE.md) décrit la
même procédure à la main.

## État

Version 0 : le format, décrit dans [SPEC.fr.md](SPEC.fr.md), et l'outil peuvent
encore changer. Pas encore de version publiée ; compilez l'outil avec
`go build -o mazetly-rescue ./cmd/mazetly-rescue`.

La recette de chaque application est testée de bout en bout en CI : sauvegardée,
restaurée sur une machine neuve avec la seule clé client (à la main et avec
l'outil), vérifiée, puis mise à jour.

| Application | Base | Recette |
|---|---|---|
| Matomo | MariaDB | [apps/matomo](apps/matomo) |
| GateCHA | SQLite | [apps/gatecha](apps/gatecha) |
| Uptime Kuma | SQLite | [apps/uptime-kuma](apps/uptime-kuma) |
| Healthchecks | PostgreSQL | [apps/healthchecks](apps/healthchecks) |
| Activepieces | PostgreSQL | [apps/activepieces](apps/activepieces) |
| Nextcloud | MariaDB | [apps/nextcloud](apps/nextcloud) |

## Licence

[Apache 2.0](LICENSE).
