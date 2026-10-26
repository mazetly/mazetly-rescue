# Vaultwarden round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/alive
BACKUP_IMAGES=(vaultwarden=vaultwarden/server:1.36.0)
UPGRADE=(vaultwarden=vaultwarden/server:1.37.4)

# What the restore must find again, written next to the kit's data at seed
# time. Test-only: a real kit has no such file.
EXPECT=volumes/rescue-test.json

prepare() {
	mkdir -p "$kit/volumes/data"
	local admin hash
	admin=$(openssl rand -hex 24)
	# The admin token is stored as an Argon2 hash, as Vaultwarden recommends;
	# single quotes keep Compose from reading its $ as variables.
	hash=$(docker run --rm "$PYTHON_IMAGE" sh -c \
		'pip install -q --root-user-action=ignore argon2-cffi==25.1.0 >/dev/null &&
		 python -c "import sys; from argon2 import PasswordHasher as H; print(H(time_cost=3, memory_cost=65540, parallelism=4).hash(sys.argv[1]))" "$1"' \
		- "$admin")
	cat >"$kit/.env" <<-EOF
		DOMAIN=$base
		ADMIN_TOKEN='$hash'
		RESCUE_TEST_ADMIN=$admin
		RESCUE_TEST_PASSWORD=$(openssl rand -hex 16)
	EOF
}

client() {
	docker run --rm --network host -v "$ROOT/apps/vaultwarden/seed.py":/seed.py:ro -v "$kit/volumes":/expect \
		"$PYTHON_IMAGE" sh -c \
		'pip install -q --root-user-action=ignore requests==2.32.5 cryptography==46.0.3 >/dev/null && python /seed.py "$@"' \
		- "$base" "$(env_value RESCUE_TEST_ADMIN)" "$(env_value RESCUE_TEST_PASSWORD)" "/expect/${EXPECT#volumes/}" "$1"
}

env_value() { grep "^$1=" "$kit/.env" | cut -d= -f2; }

seed() { client seed; }

check() {
	client check
	echo "login, a password and an attachment decrypted with the master password are there"
}
