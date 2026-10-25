# Activepieces round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/api/v1/flags
BACKUP_IMAGES=(app=ghcr.io/activepieces/activepieces:0.92.2)
UPGRADE=(app=ghcr.io/activepieces/activepieces:0.92.3)

SECRET=rescue-ci-secret-value

prepare() {
	cat >"$kit/.env" <<-EOF
		AP_POSTGRES_DATABASE=activepieces
		AP_POSTGRES_USERNAME=activepieces
		AP_POSTGRES_PASSWORD=$(openssl rand -hex 16)
		AP_ENCRYPTION_KEY=$(openssl rand -hex 16)
		AP_JWT_SECRET=$(openssl rand -hex 32)
		AP_API_KEY=$(openssl rand -hex 64)
		AP_FRONTEND_URL=$base
		RESCUE_TEST_PASSWORD=Rescue-$(openssl rand -hex 12)
	EOF
}

credentials() {
	jq -n --arg p "$(grep RESCUE_TEST_PASSWORD= "$kit/.env" | cut -d= -f2)" \
		'{email: "ci@example.org", password: $p, firstName: "Rescue", lastName: "CI", trackEvents: false, newsLetter: false}'
}

# Prints "token projectId".
session() {
	curl -fs "$base/api/v1/authentication/$1" -H 'Content-Type: application/json' -d "$(credentials)" |
		jq -r '"\(.token) \(.projectId)"'
}

api() {
	local token=$1 path=$2
	shift 2
	curl -fs "$base/api/v1/$path" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' "$@"
}

seed() {
	local token project
	read -r token project < <(session sign-up)
	api "$token" flows -d "{\"displayName\":\"Rescue CI\",\"projectId\":\"$project\"}" >/dev/null
	api "$token" variables -d "{\"projectId\":\"$project\",\"name\":\"RESCUE_CI\",\"value\":\"$SECRET\"}" >/dev/null
}

check() {
	local token project variable
	read -r token project < <(session sign-in)
	api "$token" "flows?projectId=$project" | jq -e '.data[] | select(.version.displayName == "Rescue CI")' >/dev/null
	# Stored encrypted with AP_ENCRYPTION_KEY: revealing it proves the key and
	# the encrypted data came back together.
	variable=$(api "$token" "variables?projectId=$project" | jq -r '.data[] | select(.name == "RESCUE_CI") | .id')
	[ "$(api "$token" "variables/$variable/reveal" -d '{}' | jq -r .value)" = "$SECRET" ]
	echo "sign-in, flow and encrypted variable are there"
}
