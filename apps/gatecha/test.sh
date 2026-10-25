# GateCHA round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/healthz
BACKUP_IMAGES=(gatecha=ghcr.io/upellift99/gatecha:0.12.1)
UPGRADE=(gatecha=ghcr.io/upellift99/gatecha:0.13.0)

# What the restore must find again, written next to the kit's data at seed
# time. Test-only: a real kit has no such file.
EXPECT=$kit/volumes/rescue-test.json

prepare() {
	mkdir -p "$kit/volumes/data"
	as_root chown -R 1000:1000 /kit/volumes/data
	cat >"$kit/.env" <<-EOF
		GATECHA_ADMIN_PASSWORD=$(openssl rand -hex 16)
		GATECHA_SECRET_KEY=$(openssl rand -hex 32)
	EOF
}

login() {
	curl -fs "$base/api/admin/login" -H 'Content-Type: application/json' \
		-d "{\"username\":\"admin\",\"password\":\"$(grep GATECHA_ADMIN_PASSWORD= "$kit/.env" | cut -d= -f2)\"}" |
		jq -r .token
}

seed() {
	local token key
	token=$(login)
	key=$(curl -fs "$base/api/admin/keys" -H "Authorization: Bearer $token" \
		-H 'Content-Type: application/json' -d '{"name":"Rescue CI","domain":"example.org"}')
	jq '{key_id, hmac_secret, name}' <<<"$key" >"$work/expect.json"
	as_root "cp /work/expect.json /kit/volumes/rescue-test.json"
}

check() {
	local key_id secret challenge expected
	key_id=$(jq -r .key_id "$EXPECT")
	secret=$(jq -r .hmac_secret "$EXPECT")
	# The admin password is in the database, not in .env: logging in proves the
	# admin account came back.
	curl -fs "$base/api/admin/keys" -H "Authorization: Bearer $(login)" |
		jq -e --arg k "$key_id" '(.keys // .)[] | select(.key_id == $k and .name == "Rescue CI")' >/dev/null
	# A challenge signed with the HMAC secret given at key creation: the sites
	# using this key keep working without any change.
	challenge=$(curl -fs "$base/api/v1/challenge?apiKey=$key_id" -H 'Origin: https://example.org')
	expected=$(jq -r .challenge <<<"$challenge" | tr -d '\n' |
		openssl dgst -sha256 -hmac "$secret" -hex | awk '{print $NF}')
	[ "$(jq -r .signature <<<"$challenge")" = "$expected" ] || die "challenge signature does not match the key's secret"
	echo "admin login, API key and its HMAC signature are there"
}
