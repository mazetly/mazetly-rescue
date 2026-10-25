# Nextcloud round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/status.php
# Backed up on the previous major release, restored as is, then upgraded.
BACKUP_IMAGES=(app=nextcloud:32-apache)
UPGRADE=(app=nextcloud:33-apache)

CONTENT="Rescue CI: this file must come back."

prepare() {
	mkdir -p "$kit/volumes/config" "$kit/volumes/data" "$kit/volumes/custom_apps" "$kit/volumes/themes"
	as_root 'chown -R 33:33 /kit/volumes && chmod 770 /kit/volumes/data'
	cat >"$kit/.env" <<-EOF
		MARIADB_ROOT_PASSWORD=$(openssl rand -hex 16)
		MARIADB_DATABASE=nextcloud
		MARIADB_USER=nextcloud
		MARIADB_PASSWORD=$(openssl rand -hex 16)
		RESCUE_TEST_PASSWORD=Rescue-$(openssl rand -hex 12)
	EOF
}

occ() { compose exec -T -u www-data app php occ "$@"; }

password() { grep RESCUE_TEST_PASSWORD= "$kit/.env" | cut -d= -f2; }

seed() {
	occ maintenance:install --database mysql --database-host db \
		--database-name nextcloud --database-user nextcloud \
		--database-pass "$(grep MARIADB_PASSWORD= "$kit/.env" | cut -d= -f2)" \
		--admin-user rescue --admin-pass "$(password)"
	occ config:system:set trusted_domains 1 --value=localhost
	printf '%s' "$CONTENT" | curl -fs -u "rescue:$(password)" -T - \
		"$base/remote.php/dav/files/rescue/rescue.txt"
}

check() {
	# WebDAV with the admin password: the account (in the database) and the
	# file (on disk, indexed in the database) both came back.
	[ "$(curl -fs -u "rescue:$(password)" "$base/remote.php/dav/files/rescue/rescue.txt")" = "$CONTENT" ]
	[ "$(curl -fs "$base/status.php" | jq -r .maintenance)" = false ]
	echo "account, file and its contents are there, maintenance mode is off"
}
