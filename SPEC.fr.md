# Format du kit de restauration — v0 (brouillon)

*[English](SPEC.md) · Français — traduction de `SPEC.md`, qui fait foi en cas d'écart.*

Ce document décrit ce que contient une sauvegarde Mazetly et comment on la remet en
service sans Mazetly. C'est lui qui fait référence : l'outil `mazetly-rescue` n'est qu'une façon
pratique d'appliquer cette procédure, pas une condition pour la suivre.

## Principes

1. **Le kit se suffit à lui-même.** Avec un serveur, Docker et restic, une personne
   compétente doit pouvoir restaurer l'application en lisant `RESTORE.md`, sans
   l'outil, sans compte, sans API Mazetly.
2. **Le kit est la sauvegarde.** Mazetly n'a pas d'autre format interne. Chaque
   restauration faite par Mazetly passe par ce format et par cet outil.
3. **Une restauration n'existe que si elle a été testée.** Un kit n'est déclaré
   valide qu'après une restauration automatique réussie sur une machine vierge.
4. **Aucun format propriétaire.** restic, Docker Compose, des dumps SQL standards,
   du JSON.

## Stockage : restic

Chaque instance d'application a son propre dépôt [restic](https://restic.net)
(BSD 2-Clause) sur S3 (`s3:<endpoint>/<bucket>/<instance>`).

- restic chiffre tout (AES-256 + Poly1305), compresse, dédoublonne et écrit
  nativement sur S3. Son format de dépôt est
  [documenté publiquement](https://restic.readthedocs.io/en/stable/100_references.html),
  et une seconde implémentation indépendante existe ([rustic](https://rustic.cli.rs)) :
  une sauvegarde reste lisible même sans restic.

### Clés : une par partie, jamais partagée

Les données sont chiffrées par une clé maîtresse unique, tirée au hasard à la
création du dépôt. Chaque mot de passe en est un emballage distinct
(`keys/` du dépôt) :

- **Clé Mazetly** : sert aux sauvegardes. Elle n'est jamais communiquée au client.
- **Clé client** : créée à la mise en service (`restic key add`) et remise au
  client. Mazetly n'en garde pas de copie. Le client peut en changer quand il veut
  (`restic key passwd`) sans prévenir Mazetly ni interrompre les sauvegardes.

Chacune ouvre le dépôt seule. restic n'a pas de rôles : toute clé permet de lister,
d'ajouter et de retirer les autres. **Ce sont les droits S3 qui décident** :

| Bucket | Mazetly | Client |
|---|---|---|
| Stockage Mazetly | lecture et écriture | lecture seule, limitée à son instance : il restaure (`--no-lock`), mais ne peut toucher à aucune clé |
| Bucket du client | écriture, via la clé S3 confiée par le client | propriétaire |

Dans le bucket du client, Mazetly pourrait techniquement retirer la clé du client.
Le versionnage la rend récupérable pendant 30 jours, et `mazetly-rescue check`
vérifie que la clé du client ouvre toujours le dépôt.

Retirer une clé (`restic key remove`) n'efface pas
ce qu'un ancien détenteur a pu conserver, puisque la clé maîtresse ne change pas :
pour changer vraiment la clé de chiffrement, il faut créer un nouveau dépôt.

### Copie dans le bucket du client (facultative)

Mazetly copie les instantanés dans un second dépôt restic, dans un bucket S3 fourni
par le client (`restic copy`). Ce dépôt a sa propre clé maîtresse et les deux mêmes
types de clé : celle de Mazetly pour écrire, celle du client pour restaurer.

- Le client crée le bucket et une clé d'accès S3 limitée à ce bucket. **C'est son
  vrai levier** : en révoquant cette clé chez son fournisseur, il coupe tout accès
  de Mazetly à sa copie.
- **Versionnage du bucket obligatoire**, avec suppression des versions non
  courantes après 30 jours : une suppression faite par erreur ou par un attaquant
  passé par Mazetly reste récupérable pendant 30 jours. Verrouillage des objets
  (Object Lock) en plus quand le fournisseur le propose.
  *À valider : `forget`/`prune` sur un bucket verrouillé.*
- Le stockage est facturé au client par son fournisseur. Mazetly ne facture rien
  de plus.

## Contenu d'un instantané

Chaque instantané restic contient un seul répertoire `kit/` :

```
kit/
├── manifest.json        description machine du kit (voir plus bas)
├── RESTORE.md           procédure humaine, propre à cette application
├── compose.yaml         services, images épinglées par digest
├── .env                 variables et secrets de l'instance
├── db/
│   └── <service>.sql.zst  dump logique de chaque base
└── volumes/
    └── <nom>/           contenu de chaque volume persistant
```

`.env` contient les secrets en clair **dans** l'instantané : restic chiffre
l'ensemble, il n'y a pas de seconde couche de chiffrement à gérer.

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
    { "name": "matomo-data", "path": "volumes/matomo-data", "mount": "/var/www/html" }
  ],
  "post_restore": [
    "docker compose exec -T app ./console core:update --yes"
  ],
  "consistency": "mysqldump --single-transaction"
}
```

Les images sont épinglées par digest : on restaure **exactement** la version
sauvegardée, puis on met à jour si on le souhaite, jamais l'inverse.

## Recettes par application

### Matomo (v1)

- **Sauvegarde** : `mysqldump --single-transaction --routines` (tables InnoDB,
  cohérent sans arrêt), `config/config.ini.php`, plugins tiers (`plugins/` hors
  plugins livrés), `misc/user/`.
- **Restauration** : démarrer la base, importer le dump, démarrer Matomo à la même
  version, vérifier `./console core:version`, réactiver la tâche cron
  d'archivage.
- **À adapter** si le domaine change : `trusted_hosts[]` dans `config.ini.php`.

### Nextcloud (v2)

- **Sauvegarde** : `occ maintenance:mode --on`, dump de la base, répertoire
  `data/` (y compris les clés de chiffrement côté serveur s'il est actif),
  `config/config.php` (`instanceid`, `passwordsalt`, `secret`), `custom_apps/`,
  puis `occ maintenance:mode --off`. La fenêtre de maintenance dure le temps du
  dump ; `data/` est copié ensuite, avec le risque connu de fichiers modifiés
  pendant la copie, qu'un `files:scan` corrige.
- **Restauration** : même version, import, `occ maintenance:data-fingerprint`,
  `occ files:scan --all`, adaptation de `trusted_domains` et
  `overwrite.cli.url`, Redis et cron à reconfigurer.
- **Volume** : `data/` peut peser des centaines de Go ; la première copie vers le
  bucket du client se planifie.

## Environnement cible

Debian 13 vierge, accès root, 2 Go de RAM minimum (4 Go pour Nextcloud).
L'outil installe Docker et restic depuis les dépôts officiels si besoin.

## Ce que l'outil ne fait pas

- Pas de DNS ni de certificat : il affiche ce qu'il faut configurer.
- Pas de migration de version : il restaure à l'identique.
- Pas de restauration partielle en v1.

## Décisions

- **Sauvegarde : restic**, pour ses clés multiples et son format documenté.
- **Outil : binaire Go**, un seul fichier pour Linux amd64 et arm64. restic n'est
  pas une bibliothèque (son code est en `internal/`) : l'outil appelle le binaire
  officiel de restic, qu'il télécharge et vérifie (somme SHA-256 et signature)
  s'il est absent.
- **Langues : anglais et français.** L'anglais fait foi pour la spécification
  (`SPEC.md`), le français en est la traduction (`SPEC.fr.md`). L'outil parle la
  langue du système (`LANG`), en anglais par défaut ; `RESTORE.md` est rédigé
  dans la langue du client.

- **Preuve publique** : la CI [Round trip](.github/workflows/roundtrip.yml)
  installe un Matomo de référence, produit son kit et le sauvegarde, puis le
  restaure sur une machine neuve avec la seule clé client, en suivant le
  `RESTORE.md` du kit. Elle tourne à chaque modification et chaque semaine
  (`test/matomo/roundtrip.sh`).
