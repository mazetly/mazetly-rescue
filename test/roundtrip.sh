#!/usr/bin/env bash
# Round-trip test of an application's recipe, driven by the x-mazetly fields of
# apps/<app>/compose.yaml. Nothing here knows any application: what a test
# needs to create and check data lives in apps/<app>/test.sh.
#
#   roundtrip.sh <app> seed           <kit>          start the app in <kit>, add data
#   roundtrip.sh <app> backup         <kit> <repo>   back it up with mazetly-rescue
#   roundtrip.sh <app> restore-manual <kit> <repo>   restore by hand, following
#                                                    RESTORE.md, check, upgrade
#   roundtrip.sh <app> restore-tool   <kit> <repo>   restore with mazetly-rescue, check
#
# backup needs RESTIC_PASSWORD (Mazetly's key) and RESTIC_CUSTOMER_PASSWORD;
# the restores need RESTIC_CUSTOMER_PASSWORD only. RESCUE_BIN is the tool.
set -Eeuo pipefail
trap 'echo "failed: $BASH_COMMAND (line $LINENO)" >&2' ERR

RESTIC_IMAGE=restic/restic:0.18.0
YQ_IMAGE=mikefarah/yq:4.44.3
PYTHON_IMAGE=python:3.13-alpine
ROOT=$(cd "$(dirname "$0")/.." && pwd)
RESCUE_BIN=${RESCUE_BIN:-$ROOT/bin/mazetly-rescue}

app=$1
mode=$2
kit=$(realpath -m "$3")
repo=${4:+$(realpath -m "$4")}
work=$(dirname "$kit")
base=http://localhost:${RESCUE_PORT:-8080}

log() { printf '\n==> %s\n' "$*"; }
die() { echo "$*" >&2; exit 1; }

compose() { docker compose --project-directory "$kit" "$@"; }

# Runs a shell command as root with the kit at /kit and the workdir at /work.
as_root() {
	docker run --rm -v "$kit":/kit -v "$work":/work --entrypoint sh "$RESTIC_IMAGE" -c "$*"
}

restic() {
	docker run --rm -i -e RESTIC_PASSWORD -e RESTIC_REPOSITORY=/repo \
		-v "$repo":/repo -v "$kit":/restore "$RESTIC_IMAGE" "$@"
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
	local phase=$1 service user cmd
	while IFS=$'\t' read -r -u 3 service user cmd; do
		[ -n "$service" ] || continue
		echo "hook $phase on $service: $cmd"
		compose exec -T ${user:+-u "$user"} "$service" sh -c "$cmd"
	done 3< <(jq -r --arg ph "$phase" '.services | to_entries[]
		| .key as $s | (.value."x-mazetly".hooks[$ph] // [])[]
		| if type == "string" then [$s, "", .] else [$s, (.user // ""), .run] end | @tsv' <<<"$CONFIG")
}

# The application's version, read as its recipe says.
version() {
	local service label run
	service=$(jq -r '."x-mazetly".version.service // empty' <<<"$CONFIG")
	[ -n "$service" ] || return 0
	label=$(jq -r '."x-mazetly".version.label // empty' <<<"$CONFIG")
	run=$(jq -r '."x-mazetly".version.run // empty' <<<"$CONFIG")
	if [ -n "$label" ]; then
		docker inspect -f "{{index .Config.Labels \"$label\"}}" "$(compose ps -q "$service")"
	else
		compose exec -T "$service" sh -c "$run" | tr -d '\r'
	fi
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

seed_app() {
	[ ! -e "$kit" ] || die "$kit already exists"
	mkdir -p "$kit"
	cp "$ROOT/apps/$app/compose.yaml" "$ROOT/apps/$app/NOTES.md" "$kit/"
	prepare
	CONFIG=$(config)
	local pair
	for pair in "${BACKUP_IMAGES[@]}"; do set_image "${pair%%=*}" "${pair#*=}"; done

	log "Reference $app"
	compose up -d --wait --wait-timeout 600
	wait_ready
	seed
	check
}

backup() {
	: "${RESTIC_PASSWORD:?}" "${RESTIC_CUSTOMER_PASSWORD:?}"
	log "mazetly-rescue init and backup"
	printf %s "$RESTIC_CUSTOMER_PASSWORD" >"$work/customer-key"
	"$RESCUE_BIN" init --repo "$repo" --customer-key-file "$work/customer-key"
	rm "$work/customer-key"
	"$RESCUE_BIN" backup --repo "$repo" "$kit"
	# The instance is gone after this: restores start from the repository alone.
	compose down -v
	# Root-owned repository files must be readable to be uploaded as an artifact.
	docker run --rm -v "$repo":/repo --entrypoint chown "$RESTIC_IMAGE" -R "$(id -u):$(id -g)" /repo
}

restore_manual() {
	: "${RESTIC_CUSTOMER_PASSWORD:?}"
	export RESTIC_PASSWORD=$RESTIC_CUSTOMER_PASSWORD
	[ ! -e "$kit" ] || die "$kit already exists"
	mkdir -p "$kit"

	log "1. Restore the kit with the customer key"
	local snapshot id path
	snapshot=$(restic snapshots --no-lock --json --tag mazetly-kit | jq -c '.[-1]')
	id=$(jq -r .id <<<"$snapshot")
	path=$(jq -r '.paths[0]' <<<"$snapshot")
	restic restore "$id:$path" --no-lock --target /restore

	log "2. Check the database exports"
	(cd "$kit" && jq -r '.databases[] | "\(.sha256)  \(.file)"' manifest.json | sha256sum -c)

	log "3. Pin the images"
	CONFIG=$(config)
	local service image engine
	while read -r -u 3 service image; do
		set_image "$service" "$image"
	done 3< <(jq -r '.images | to_entries[] | "\(.key) \(.value)"' "$kit/manifest.json")

	log "4. Restore each database"
	while read -r -u 3 service engine path; do
		case $engine in
		sqlite)
			local rel
			rel=$(host_path "$service" "$path" | sed "s#^$kit/##")
			as_root "cp '/kit/db/$service.sqlite' '/kit/$rel' && chown \$(stat -c %u:%g \"\$(dirname '/kit/$rel')\") '/kit/$rel'"
			;;
		*)
			compose up -d --wait --wait-timeout 600 "$service"
			case $engine in
			mariadb) compose exec -T "$service" sh -c 'exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"' <"$kit/db/$service.sql" ;;
			mysql) compose exec -T "$service" sh -c 'exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"' <"$kit/db/$service.sql" ;;
			postgres) compose exec -T "$service" sh -c 'exec psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}"' <"$kit/db/$service.sql" >/dev/null ;;
			esac
			;;
		esac
	done 3< <(databases)

	log "5. Start everything"
	compose up -d --wait --wait-timeout 600
	wait_ready

	log "6. Post-restore commands"
	run_hooks post_restore

	log "Checks: same version, same data"
	local expected actual
	expected=$(jq -r .app_version "$kit/manifest.json")
	actual=$(version)
	[ "$actual" = "$expected" ] || die "version $actual, expected $expected"
	check
	echo "$app $actual restored by hand with the customer key only."

	[ "${#UPGRADE[@]}" -gt 0 ] || return 0
	log "Upgrade, following NOTES.md"
	local pair renew upgraded
	for pair in "${UPGRADE[@]}"; do
		service=${pair%%=*}
		set_image "$service" "${pair#*=}"
		renew=$(jq -r --arg s "$service" '.services[$s]."x-mazetly".upgrade.renew_anon_volumes // false' <<<"$CONFIG")
		compose up -d --wait --wait-timeout 600 $([ "$renew" = true ] && echo --renew-anon-volumes) "$service"
	done
	wait_ready
	run_hooks post_upgrade
	upgraded=$(version)
	[ "$upgraded" != "$actual" ] || die "still on $actual after the upgrade"
	check
	echo "$app upgraded from $actual to $upgraded with its data intact."
}

restore_tool() {
	: "${RESTIC_CUSTOMER_PASSWORD:?}"
	log "mazetly-rescue check, then restore, with the customer key"
	RESTIC_PASSWORD=$RESTIC_CUSTOMER_PASSWORD "$RESCUE_BIN" check --repo "$repo"
	RESTIC_PASSWORD=$RESTIC_CUSTOMER_PASSWORD "$RESCUE_BIN" restore --repo "$repo" --target "$kit"
	CONFIG=$(config)
	wait_ready
	log "Checks"
	check
	echo "$app $(version) restored by mazetly-rescue with the customer key only."
}

UPGRADE=()
BACKUP_IMAGES=()
# shellcheck source=/dev/null
. "$ROOT/apps/$app/test.sh"

case $mode in
seed) seed_app ;;
backup) backup ;;
restore-manual) restore_manual ;;
restore-tool) restore_tool ;;
*) die "usage: $0 <app> seed|backup|restore-manual|restore-tool <kit> [<repo>]" ;;
esac
