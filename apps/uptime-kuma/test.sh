# Uptime Kuma round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/
BACKUP_IMAGES=(kuma=louislam/uptime-kuma:2.0.0)
UPGRADE=(kuma=louislam/uptime-kuma:2)

prepare() {
	mkdir -p "$kit/volumes/data"
	# The admin password lives in the database; the test keeps it in .env so
	# that the restore can log in again.
	echo "RESCUE_TEST_PASSWORD=$(openssl rand -hex 16)" >"$kit/.env"
}

version() { compose exec -T kuma node -p 'require("./package.json").version' | tr -d '\r'; }

socket() {
	docker run --rm --network host -v "$ROOT/apps/uptime-kuma/seed.py":/seed.py:ro "$PYTHON_IMAGE" \
		sh -c 'pip install -q --root-user-action=ignore "python-socketio[client]==5.17.0" && python /seed.py "$@"' \
		- "$base" "$(grep RESCUE_TEST_PASSWORD= "$kit/.env" | cut -d= -f2)" "$1"
}

seed() { socket seed; }

check() {
	socket check
	echo "admin login and monitor are there"
}
