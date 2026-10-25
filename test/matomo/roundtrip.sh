#!/usr/bin/env bash
# End-to-end check of the kit format for Matomo.
#
#   roundtrip.sh backup  <workdir> <repo>   install a reference Matomo, produce a
#                                           kit, back it up with both keys
#   roundtrip.sh restore <workdir> <repo>   restore the kit with the customer key
#                                           only and check the data came back
#
# backup needs RESTIC_MAZETLY_PASSWORD and RESTIC_CUSTOMER_PASSWORD;
# restore needs RESTIC_CUSTOMER_PASSWORD only.
set -euo pipefail

RESTIC_IMAGE=restic/restic:0.18.0
SITE_NAME="Rescue CI"
HERE=$(cd "$(dirname "$0")" && pwd)

mode=$1
work=$(realpath -m "$2")
repo=$(realpath -m "$3")
kit=$work/kit

log() { printf '\n==> %s\n' "$*"; }

restic() {
	local password=$1
	shift
	docker run --rm -e RESTIC_PASSWORD="$password" -e RESTIC_REPOSITORY=/repo \
		-v "$repo":/repo -v "$work":/work "$RESTIC_IMAGE" "$@"
}

compose() { docker compose --project-directory "$kit" "$@"; }

wait_http() {
	for _ in $(seq 1 90); do
		curl -fs -o /dev/null "$1" && return 0
		sleep 2
	done
	echo "timeout waiting for $1" >&2
	return 1
}

# Drives the web installer: Matomo has no console installer.
install_matomo() {
	local jar=$work/cookies p=http://localhost:8080/index.php
	local post=(curl -fs -o /dev/null -c "$jar" -b "$jar")
	"${post[@]}" "$p?action=systemCheck&module=Installation"
	"${post[@]}" -d _qf__databasesetupform= -d host=db -d username="$MARIADB_USER" \
		-d password="$MARIADB_PASSWORD" -d dbname="$MARIADB_DATABASE" \
		-d tables_prefix=matomo_ --data-urlencode 'adapter=PDO\MYSQL' \
		-d schema=Mariadb -d submit=Next "$p?action=databaseSetup&module=Installation"
	"${post[@]}" "$p?action=tablesCreation&module=Installation"
	"${post[@]}" -d _qf__generalsetupform= -d login=rescue \
		-d password="$MATOMO_ADMIN_PASSWORD" -d password_bis="$MATOMO_ADMIN_PASSWORD" \
		-d email=ci@example.org -d submit=Next "$p?action=setupSuperUser&module=Installation"
	"${post[@]}" -d _qf__websitesetupform= -d siteName="$SITE_NAME" \
		-d url=https://example.org -d timezone=Europe/Paris -d ecommerce=0 \
		-d submit=Next "$p?action=firstWebsiteSetup&module=Installation"
	"${post[@]}" -d _qf__defaultsettingsform= -d setup_geoip2=0 -d anonymise_ip=1 \
		-d submit=Continue "$p?action=finished&module=Installation"
	rm -f "$jar"
}

db_query() {
	compose exec -T db mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" \
		"$MARIADB_DATABASE" -N -e "$1"
}

backup() {
	: "${RESTIC_MAZETLY_PASSWORD:?}" "${RESTIC_CUSTOMER_PASSWORD:?}"
	mkdir -p "$kit/db" "$kit/volumes/matomo-data" "$repo"
	cp "$HERE/compose.yaml" "$HERE/RESTORE.md" "$kit/"
	cat >"$kit/.env" <<-EOF
		MARIADB_ROOT_PASSWORD=$(openssl rand -hex 16)
		MARIADB_DATABASE=matomo
		MARIADB_USER=matomo
		MARIADB_PASSWORD=$(openssl rand -hex 16)
		MATOMO_ADMIN_PASSWORD=$(openssl rand -hex 16)
	EOF
	set -a && . "$kit/.env" && set +a

	log "Reference Matomo"
	compose up -d --wait db
	compose up -d app
	wait_http http://localhost:8080/index.php
	install_matomo
	curl -fs -o /dev/null "http://localhost:8080/matomo.php?idsite=1&rec=1&url=https://example.org/&action_name=rescue-ci"
	[ "$(db_query 'select count(*) from matomo_log_visit')" = 1 ]

	log "Kit"
	compose exec -T db mariadb-dump --single-transaction --routines \
		-u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE" | zstd -q >"$kit/db/db.sql.zst"
	local version
	version=$(compose exec -T app php console core:version | tr -d '\r')
	# The instance is gone after this: restore starts from the repository alone.
	compose down -v
	for image in mariadb:11.4 matomo:5-apache; do
		sed -i "s#image: $image\$#image: $(docker image inspect --format '{{index .RepoDigests 0}}' "$image")#" "$kit/compose.yaml"
	done
	jq -n --arg version "$version" --arg sha "$(sha256sum "$kit/db/db.sql.zst" | cut -d' ' -f1)" \
		--arg now "$(date -u +%FT%TZ)" \
		--arg app "$(grep -oE 'matomo@sha256:[0-9a-f]+' "$kit/compose.yaml")" \
		--arg db "$(grep -oE 'mariadb@sha256:[0-9a-f]+' "$kit/compose.yaml")" '{
			format: "mazetly-kit/0", app: "matomo", app_version: $version,
			created_at: $now, instance: "rescue-ci", domain: "localhost",
			images: { app: $app, db: $db },
			databases: [{ service: "db", engine: "mariadb", file: "db/db.sql.zst", sha256: $sha }],
			volumes: [{ name: "matomo-data", path: "volumes/matomo-data", mount: "/var/www/html" }],
			post_restore: ["docker compose exec -T app php console core:update --yes"],
			consistency: "mysqldump --single-transaction"
		}' >"$kit/manifest.json"
	cat "$kit/manifest.json"

	log "restic: Mazetly key, customer key, snapshot"
	restic "$RESTIC_MAZETLY_PASSWORD" init
	docker run --rm -e RESTIC_PASSWORD="$RESTIC_MAZETLY_PASSWORD" -e RESTIC_REPOSITORY=/repo \
		-e NEW="$RESTIC_CUSTOMER_PASSWORD" -v "$repo":/repo --entrypoint sh "$RESTIC_IMAGE" \
		-c 'printf %s "$NEW" > /tmp/new && restic key add --new-password-file /tmp/new --user customer --host rescue'
	restic "$RESTIC_MAZETLY_PASSWORD" backup --host rescue-ci /work/kit
	restic "$RESTIC_MAZETLY_PASSWORD" key list
	# Root-owned repository files must be readable to be uploaded as an artifact.
	docker run --rm -v "$repo":/repo --entrypoint chown "$RESTIC_IMAGE" -R "$(id -u):$(id -g)" /repo
}

restore() {
	: "${RESTIC_CUSTOMER_PASSWORD:?}"
	if [ -n "${RESTIC_MAZETLY_PASSWORD:-}" ]; then
		echo "restore must run with the customer key only" >&2
		exit 1
	fi
	[ ! -e "$kit" ] || { echo "$kit already exists" >&2; exit 1; }
	mkdir -p "$work"

	log "restic restore with the customer key"
	restic "$RESTIC_CUSTOMER_PASSWORD" restore latest:/work/kit --no-lock --target /work/kit
	# restic creates the target itself as root, mode 0700; its contents keep
	# their original owners. RESTORE.md runs as root, this script does not.
	docker run --rm -v "$work":/work --entrypoint sh "$RESTIC_IMAGE" \
		-c "chown $(id -u):$(id -g) /work/kit && chmod 755 /work/kit"

	log "Restore, following RESTORE.md"
	(cd "$kit" && jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c)
	set -a && . "$kit/.env" && set +a
	compose up -d --wait db
	zstd -dc "$kit/db/db.sql.zst" | compose exec -T db \
		mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"
	compose up -d app
	wait_http http://localhost:8080/index.php
	(cd "$kit" && jq -r '.post_restore[]' manifest.json | sh)

	log "Checks"
	local expected actual
	expected=$(jq -r .app_version "$kit/manifest.json")
	actual=$(compose exec -T app php console core:version | tr -d '\r')
	[ "$actual" = "$expected" ] || { echo "version $actual, expected $expected" >&2; exit 1; }
	[ "$(db_query 'select name from matomo_site where idsite = 1')" = "$SITE_NAME" ]
	[ "$(db_query 'select count(*) from matomo_log_visit')" = 1 ]
	curl -fs http://localhost:8080/index.php | grep -c 'Sign in' >/dev/null
	echo "Matomo $actual restored: site, visit and login page are back."
}

case $mode in
backup) backup ;;
restore) restore ;;
*) echo "usage: $0 backup|restore <workdir> <repo>" >&2; exit 2 ;;
esac
