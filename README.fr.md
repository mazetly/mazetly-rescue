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
- un serveur Debian vierge.

## État

Projet en conception. Le format des sauvegardes est décrit dans [SPEC.fr.md](SPEC.fr.md).

| Application | État |
|---|---|
| Matomo | format du kit testé en CI ; outil prévu (v1) |
| Nextcloud | prévu (v2) |

## Licence

[Apache 2.0](LICENSE).
