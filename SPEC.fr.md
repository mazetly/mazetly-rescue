# Format du kit de restauration — v0 (brouillon)

*[English](SPEC.md) · Français — traduction de `SPEC.md`, qui fait foi en cas d'écart.*

Ce document décrit ce que contient une sauvegarde Mazetly et comment on la remet en
service sans Mazetly. C'est lui qui fait référence : l'outil `mazetly-rescue` n'est qu'une façon
pratique d'appliquer cette procédure, pas une condition pour la suivre.

## Principes

1. **Le kit se suffit à lui-même.** Avec un serveur, Docker, restic et jq, une personne
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

Le répertoire d'une instance est son kit. Chaque instantané restic le contient en
entier :

```
kit/
├── compose.yaml   la recette : services, montages et champs x-mazetly,
│                  images épinglées par digest
├── .env           réglages et secrets
├── db/            un export par base (<service>.sql ou
│                  <service>.sqlite)
├── volumes/       ce qu'un utilisateur peut modifier, monté depuis ici
├── manifest.json  ce que la sauvegarde a produit (voir plus bas)
├── RESTORE.md     la procédure générique
└── NOTES.md       ce qui est propre à l'application
```

`.env` contient les secrets en clair **dans** l'instantané : restic chiffre
l'ensemble, il n'y a pas de seconde couche de chiffrement à gérer. Les fichiers
de base vivants sous `volumes/` (SQLite et ses `-wal`/`-shm`) sont exclus : `db/`
en contient la copie cohérente. Les exports ne sont pas compressés : restic
compresse et déduplique, et un dump SQL qui a peu changé d'un jour à l'autre ne
coûte presque rien à stocker à nouveau, quand un dump compressé serait chaque
fois une donnée nouvelle.

## Recettes : `x-mazetly` dans `compose.yaml`

L'outil ne connaît aucune application. Une recette est le `compose.yaml` de
l'application, dans [`apps/<app>/`](apps), avec des champs `x-mazetly` là où
Compose n'a pas de mot pour ce dont une sauvegarde a besoin. Docker les ignore :
le fichier se lance tel quel avec `docker compose up`.

- **Le code vient de l'image, jamais de la sauvegarde.** Seul ce qu'un
  utilisateur peut modifier est monté, depuis `./volumes/`. Tout ce qui s'y
  trouve est sauvegardé ; les volumes nommés ne le sont pas, les bases passent
  donc par un export.
- **`x-mazetly.database`** sur un service de base : `engine` vaut `mariadb`,
  `mysql`, `postgres` ou `sqlite`. L'outil sait exporter et importer chacun ; les
  identifiants viennent des variables habituelles des images (`MARIADB_*`,
  `MYSQL_*`, `POSTGRES_*`). Pour `sqlite`, `path` est le fichier dans le
  conteneur ; il est copié par l'API de sauvegarde à chaud de SQLite, cohérente
  pendant que l'application écrit.
- **`x-mazetly.hooks`** sur n'importe quel service : des commandes lancées dans
  ce service, en texte ou `{user, run}`, aux étapes `pre_backup`, `post_backup`,
  `post_restore` et `post_upgrade`.
- **`x-mazetly.upgrade.renew_anon_volumes`** : l'image ne copie son code qu'une
  fois, dans un volume anonyme ; une nouvelle image demande
  `docker compose up --renew-anon-volumes`.
- **`$` dans les commandes** : Compose interprète aussi les champs `x-*` ; une
  recette écrit donc `$$` pour un `$` littéral, et `docker compose config`
  l'affiche toujours échappé : l'outil remet `$$` en `$` avant de lancer une
  commande.
- **`x-mazetly` en haut du fichier** : `app`, le nom de l'application, et
  `version`, la façon de lire la version qui tourne : `{service, run}` (la
  sortie d'une commande) ou `{service, label}` (une étiquette de l'image).
- **Healthchecks** : tout service qui met du temps à être prêt déclare un
  `healthcheck` Compose, pour que `docker compose up --wait` ne rende la main
  qu'une fois l'application en état de répondre.

Ce qu'aucun champ ne sait dire (adaptations à un nouveau domaine, particularités
d'une application) va en texte dans `NOTES.md`.

Prendre en charge une nouvelle application, c'est écrire son `compose.yaml`, son
`NOTES.md` et son test d'aller-retour (`apps/<app>/test.sh`) : quoi créer, quoi
retrouver. Le script de test, [`test/roundtrip.sh`](test/roundtrip.sh), est le
même pour toutes les applications et ne lit que `x-mazetly`.

## `manifest.json`

Ce que la sauvegarde a produit ; tout le reste est dans `compose.yaml`.

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

Les images sont épinglées par digest : on restaure **exactement** la version
sauvegardée, puis on met à jour si on le souhaite, jamais l'inverse.

## Applications

Chacune est sauvegardée sur une version antérieure, restaurée sur une machine
neuve avec la seule clé client, vérifiée, puis mise à jour, à chaque modification.

| Application | Base | Ce que le test prouve | Mise à jour testée |
|---|---|---|---|
| [Matomo](apps/matomo) | MariaDB | site, visites, un plugin tiers, un logo personnalisé | 5.13 → 5.14 |
| [GateCHA](apps/gatecha) | SQLite | connexion admin, une clé d'API, sa signature HMAC inchangée | 0.12 → 0.13 |
| [Uptime Kuma](apps/uptime-kuma) | SQLite | connexion admin, une sonde | 2.0.0 → 2.x |
| [Healthchecks](apps/healthchecks) | PostgreSQL | utilisateur, un check, son URL de ping et son historique | v4.4 → v5.0 |
| [Activepieces](apps/activepieces) | PostgreSQL | connexion, un flow, une variable chiffrée avec `AP_ENCRYPTION_KEY` | 0.92.2 → 0.92.3 |
| [Nextcloud](apps/nextcloud) | MariaDB | compte, un fichier via WebDAV, sauvegarde en mode maintenance | 32 → 33 |

Ce qu'elles ont appris au format :

- **Matomo** : la Marketplace installe les plugins au milieu du code de Matomo ;
  les plugins tiers ont donc leur propre dossier (`MATOMO_PLUGIN_DIRS`). L'image
  demande `--renew-anon-volumes` pour une mise à jour.
- **GateCHA, Uptime Kuma** : un fichier SQLite vivant ne se copie pas tel quel ;
  il est exclu et remplacé par une copie cohérente.
- **Activepieces** : une clé de chiffrement dans `.env` et les données qu'elle
  chiffre doivent revenir ensemble ; le test déchiffre une valeur pour le prouver.
- **Nextcloud** : des hooks encadrent la sauvegarde (mode maintenance) ; après
  une mise à jour, `post_restore` ne doit pas tourner (il forcerait tous les
  clients de synchronisation à tout revérifier), d'où `post_upgrade`.

## Environnement cible

Debian 13 vierge, accès root, 2 Go de RAM minimum (4 Go pour Nextcloud).
L'outil n'a besoin que de Docker : restic et SQLite tournent depuis leurs images
officielles, épinglées par digest.

## Ce que l'outil ne fait pas

- Pas de DNS ni de certificat : il affiche ce qu'il faut configurer.
- Pas de migration de version : il restaure à l'identique.
- Pas de restauration partielle en v1.

## Décisions

- **Sauvegarde : restic**, pour ses clés multiples et son format documenté.
- **Recettes : des champs `x-mazetly` dans `compose.yaml`**, pas un fichier à
  part qu'il faudrait garder synchronisé avec lui.
- **Outil : binaire Go**, un seul fichier pour Linux amd64 et arm64, avec deux
  dépendances (`gopkg.in/yaml.v3`, `golang.org/x/term`). restic n'est pas une
  bibliothèque (son code est en `internal/`) : l'outil fait tourner l'image
  officielle de restic, épinglée par digest ; un serveur n'a donc besoin que de
  Docker. `docker compose config --format json` perd les champs `x-*` : l'outil
  lit la sortie YAML.
- **Commandes** : `restore` (les étapes de `RESTORE.md`), `check` (la clé ouvre
  le dépôt et les derniers exports se relisent intacts ; `--max-age` pour la
  supervision), `backup` (côté Mazetly) et `init` (un dépôt avec une clé
  client).
- **Langues : anglais et français.** L'anglais fait foi pour la spécification
  (`SPEC.md`), le français en est la traduction (`SPEC.fr.md`). L'outil parle la
  langue du système (`LANG`), en anglais par défaut ; `RESTORE.md` est rédigé
  dans la langue du client.
- **Preuve publique** : la CI [Round trip](.github/workflows/roundtrip.yml)
  sauvegarde chaque application avec `mazetly-rescue backup`, puis la restaure
  deux fois sur des machines neuves avec la seule clé client : à la main en
  suivant `RESTORE.md` (puis la met à jour), et avec `mazetly-rescue restore`. À
  chaque modification et chaque semaine.
