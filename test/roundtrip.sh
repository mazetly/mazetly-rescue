#!/usr/bin/env bash
# Round-trip test of an application's recipe, driven by the x-mazetly fields of
# apps/<app>/compose.yaml. Nothing here knows any application: what a test
# needs to create and check data lives in apps/<app>/test.sh.
#
#   roundtrip.sh <app> backup  <workdir> <repo>   run the app, seed data, back up
#   roundtrip.sh <app> restore <workdir> <repo>   restore with the customer key
#                                                 only, check, then upgrade
#
# backup needs RESTIC_MAZETLY_PASSWORD and RESTIC_CUSTOMER_PASSWORD;
# restore needs RESTIC_CUSTOMER_PASSWORD only.
set -Eeuo pipefail
trap 'echo "failed: $BASH_COMMAND (line $LINENO)" >&2' ERR

RESTIC_IMAGE=restic/restic:0.18.0
YQ_IMAGE=mikefarah/yq:4.44.3
PYTHON_IMAGE=python:3.13-alpine
ROOT=$(cd "$(dirname "$0")/.." && pwd)

app=$1
mode=$2
work=$(realpath -m "$3")
repo=$(realpath -m "$4")
kit=$work/kit
base=http://localhost:${RESCUE_PORT:-8080}

log() { printf '\n==> %s\n' "$*"; }
die() { echo "$*" >&2; exit 1; }

compose() { docker compose --project-directory "$kit" -p "rescue-$app" "$@"; }

# Runs a shell command as root with the kit at /kit and the workdir at /work.
as_root() {
	docker run --rm -v "$kit":/kit -v "$work":/work --entrypoint sh "$RESTIC_IMAGE" -c "$*"
}

restic() {
	local password=$1
	shift
	docker run --rm -e RESTIC_PASSWORD="$password" -e RESTIC_REPOSITORY=/repo \
		-v "$repo":/repo -v "$work":/work "$RESTIC_IMAGE" "$@"
}

# The normalized compose file as JSON. `config --format json` drops x-* fields,
# its YAML output keeps them.
config() { compose config | docker run --rm -i "$YQ_IMAGE" -o json; }

# Services declaring a database, as "service engine path" lines.
databases() {
	jq -r '.services | to_entries[] | select(.value."x-mazetly".database)
		| "\(.key) \(.value."x-mazetly".database.engine) \(.value."x-mazetly".database.path // "")"' <<<"$CONFIG"
}

# Host path of a file inside a service, through the service's bind mounts.
host_path() {
	jq -r --arg s "$1" --arg p "$2" '.services[$s].volumes[] | select(.type == "bind") | . as $v
		| select($p | startswith($v.target + "/")) | $v.source + ($p | ltrimstr($v.target))' <<<"$CONFIG" | head -1
}

run_hooks() {
	local phase=$1 service hook user cmd
	while IFS=$'\t' read -r -u 3 service user cmd; do
		[ -n "$service" ] || continue
		echo "hook $phase on $service: $cmd"
		compose exec -T ${user:+-u "$user"} "$service" sh -c "$cmd"
	done 3< <(jq -r --arg ph "$phase" '.services | to_entries[]
		| .key as $s | (.value."x-mazetly".hooks[$ph] // [])[]
		| if type == "string" then [$s, "", .] else [$s, (.user // ""), .run] end | @tsv' <<<"$CONFIG")
}

wait_ready() {
	for _ in $(seq 1 150); do
		curl -fs -o /dev/null "$base$READY_PATH" && return 0
		sleep 2
	done
	compose logs --tail 50 >&2
	die "timeout waiting for $base$READY_PATH"
}

set_image() {
	local service=$1 image=$2 current
	current=$(jq -r --arg s "$service" '.services[$s].image' <<<"$CONFIG")
	sed -i "s#image: $current\$#image: $image#" "$kit/compose.yaml"
	grep -q "image: $image\$" "$kit/compose.yaml" || die "could not set $service image to $image"
	CONFIG=$(config)
}

dump_db() {
	local service=$1 engine=$2 path=$3 out
	case $engine in
	mariadb)
		out=db/$service.sql.zst
		compose exec -T "$service" sh -c 'exec mariadb-dump --single-transaction --routines \
			-u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"' | zstd -q >"$kit/$out"
		;;
	mysql)
		out=db/$service.sql.zst
		compose exec -T "$service" sh -c 'exec mysqldump --single-transaction --routines \
			-u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"' | zstd -q >"$kit/$out"
		;;
	postgres)
		out=db/$service.sql.zst
		compose exec -T "$service" sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"' |
			zstd -q >"$kit/$out"
		;;
	sqlite)
		# SQLite's online backup API: consistent while the application writes.
		out=db/$service.sqlite.zst
		local src
		src=$(host_path "$service" "$path")
		[ -n "$src" ] || die "$path is not under a bind mount of $service"
		docker run --rm -v "$(dirname "$src")":/src:ro -v "$kit/db":/out "$PYTHON_IMAGE" python -c "
import sqlite3
src = sqlite3.connect('file:/src/$(basename "$src")?mode=ro', uri=True)
dst = sqlite3.connect('/out/$service.sqlite')
src.backup(dst)
dst.close()"
		zstd -q --rm "$kit/db/$service.sqlite" -o "$kit/$out"
		;;
	*) die "unknown database engine: $engine" ;;
	esac
	jq -n --arg s "$service" --arg e "$engine" --arg f "$out" --arg p "$path" \
		--arg sha "$(sha256sum "$kit/$out" | cut -d' ' -f1)" \
		'{service: $s, engine: $e, file: $f, sha256: $sha} + (if $p == "" then {} else {path: $p} end)'
}

restore_db() {
	local service=$1 engine=$2 path=$3
	case $engine in
	mariadb)
		zstd -dc "$kit/db/$service.sql.zst" | compose exec -T "$service" sh -c \
			'exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"'
		;;
	mysql)
		zstd -dc "$kit/db/$service.sql.zst" | compose exec -T "$service" sh -c \
			'exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"'
		;;
	postgres)
		zstd -dc "$kit/db/$service.sql.zst" | compose exec -T "$service" sh -c \
			'exec psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"' >/dev/null
		;;
	sqlite)
		local dst rel
		dst=$(host_path "$service" "$path")
		rel=${dst#"$kit"/}
		zstd -dc "$kit/db/$service.sqlite.zst" >"$work/restored.sqlite"
		# Same owner as the directory the application writes to.
		as_root "mv /work/restored.sqlite '/kit/$rel' && chown \$(stat -c %u:%g \"\$(dirname '/kit/$rel')\") '/kit/$rel'"
		;;
	esac
}

backup() {
	: "${RESTIC_MAZETLY_PASSWORD:?}" "${RESTIC_CUSTOMER_PASSWORD:?}"
	[ ! -e "$kit" ] || die "$kit already exists"
	mkdir -p "$kit/db" "$repo"
	cp "$ROOT/apps/$app/compose.yaml" "$ROOT/apps/$app/NOTES.md" "$ROOT/kit/RESTORE.md" "$kit/"
	prepare
	CONFIG=$(config)
	local pair
	for pair in "${BACKUP_IMAGES[@]}"; do set_image "${pair%%=*}" "${pair#*=}"; done

	log "Reference $app"
	compose up -d --wait --wait-timeout 300
	wait_ready
	seed
	check

	log "Kit"
	run_hooks pre_backup
	local dbs=() service engine path
	while read -r -u 3 service engine path; do
		dbs+=("$(dump_db "$service" "$engine" "$path")")
	done 3< <(databases)
	# Pin every image to the digest that actually ran.
	local images='{}' image digest
	while read -r -u 3 service image; do
		digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "$image")
		set_image "$service" "$digest"
		images=$(jq --arg s "$service" --arg d "$digest" '. + {($s): $d}' <<<"$images")
	done 3< <(jq -r '.services | to_entries[] | "\(.key) \(.value.image)"' <<<"$CONFIG")
	jq -n --arg app "$app" --arg version "$(version 2>/dev/null || true)" \
		--arg now "$(date -u +%FT%TZ)" --argjson images "$images" \
		--argjson databases "$(printf '%s\n' "${dbs[@]}" | jq -s .)" '{
			format: "mazetly-kit/0", app: $app, app_version: $version,
			created_at: $now, images: $images, databases: $databases
		}' >"$kit/manifest.json"
	cat "$kit/manifest.json"

	log "restic: Mazetly key, customer key, snapshot of the running instance"
	restic "$RESTIC_MAZETLY_PASSWORD" init
	docker run --rm -e RESTIC_PASSWORD="$RESTIC_MAZETLY_PASSWORD" -e RESTIC_REPOSITORY=/repo \
		-e NEW="$RESTIC_CUSTOMER_PASSWORD" -v "$repo":/repo --entrypoint sh "$RESTIC_IMAGE" \
		-c 'printf %s "$NEW" > /tmp/new && restic key add --new-password-file /tmp/new --user customer --host rescue'
	# Live SQLite files are replaced by their consistent copy in db/.
	local excludes=() live
	while read -r -u 3 service engine path; do
		[ "$engine" = sqlite ] || continue
		live=/work/kit/$(host_path "$service" "$path" | sed "s#^$kit/##")
		excludes+=(--exclude "$live" --exclude "$live-wal" --exclude "$live-shm" --exclude "$live-journal")
	done 3< <(databases)
	restic "$RESTIC_MAZETLY_PASSWORD" backup --host "rescue-$app" "${excludes[@]}" /work/kit
	restic "$RESTIC_MAZETLY_PASSWORD" ls latest /work/kit/db
	run_hooks post_backup

	# The instance is gone after this: restore starts from the repository alone.
	compose down -v
	# Root-owned repository files must be readable to be uploaded as an artifact.
	docker run --rm -v "$repo":/repo --entrypoint chown "$RESTIC_IMAGE" -R "$(id -u):$(id -g)" /repo
}

restore() {
	: "${RESTIC_CUSTOMER_PASSWORD:?}"
	[ -z "${RESTIC_MAZETLY_PASSWORD:-}" ] || die "restore must run with the customer key only"
	[ ! -e "$kit" ] || die "$kit already exists"
	mkdir -p "$work"

	log "restic restore with the customer key"
	restic "$RESTIC_CUSTOMER_PASSWORD" restore latest:/work/kit --no-lock --target /work/kit
	# restic creates the target itself as root, mode 0700; its contents keep
	# their original owners. RESTORE.md runs as root, this script does not.
	as_root "chown $(id -u):$(id -g) /kit && chmod 755 /kit"

	log "Restore, following RESTORE.md"
	(cd "$kit" && jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c)
	CONFIG=$(config)
	local service engine path
	while read -r -u 3 service engine path; do
		case $engine in
		sqlite) restore_db "$service" "$engine" "$path" ;;
		*)
			compose up -d --wait --wait-timeout 300 "$service"
			restore_db "$service" "$engine" "$path"
			;;
		esac
	done 3< <(databases)
	compose up -d --wait --wait-timeout 300
	wait_ready
	run_hooks post_restore

	log "Checks: same version, same data"
	local expected actual
	expected=$(jq -r .app_version "$kit/manifest.json")
	actual=$(version 2>/dev/null || true)
	[ "$actual" = "$expected" ] || die "version $actual, expected $expected"
	check
	echo "$app ${actual:-} restored with the customer key only."

	[ "${#UPGRADE[@]}" -gt 0 ] || return 0
	log "Upgrade, following NOTES.md"
	local pair renew
	for pair in "${UPGRADE[@]}"; do
		service=${pair%%=*}
		set_image "$service" "${pair#*=}"
		renew=$(jq -r --arg s "$service" '.services[$s]."x-mazetly".upgrade.renew_anon_volumes // false' <<<"$CONFIG")
		compose up -d --wait --wait-timeout 300 $([ "$renew" = true ] && echo --renew-anon-volumes) "$service"
	done
	wait_ready
	run_hooks post_upgrade
	local upgraded
	upgraded=$(version 2>/dev/null || true)
	[ -z "$actual" ] || [ "$upgraded" != "$actual" ] || die "still on $actual after the upgrade"
	check
	echo "$app upgraded from $actual to $upgraded with its data intact."
}

UPGRADE=()
BACKUP_IMAGES=()
version() { return 1; }
# shellcheck source=/dev/null
. "$ROOT/apps/$app/test.sh"

case $mode in
backup) backup ;;
restore) restore ;;
*) die "usage: $0 <app> backup|restore <workdir> <repo>" ;;
esac
