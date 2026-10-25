# Matomo round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/index.php
# Backed up on an older release, restored as is, then upgraded.
BACKUP_IMAGES=(app=matomo:5.13.0-apache)
UPGRADE=(app=matomo:5-apache)

PLUGIN=QueuedTracking
LOGO_B64=iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==
SITE_NAME="Rescue CI"

prepare() {
	mkdir -p "$kit/volumes/config" "$kit/volumes/plugins" "$kit/volumes/misc-user"
	# Mounted as a single file: it must exist, or Docker creates a directory.
	touch "$kit/volumes/config/config.ini.php"
	echo "$LOGO_B64" | base64 -d >"$kit/volumes/misc-user/logo.png"
	as_root chown -R 33:33 /kit/volumes
	cat >"$kit/.env" <<-EOF
		MARIADB_ROOT_PASSWORD=$(openssl rand -hex 16)
		MARIADB_DATABASE=matomo
		MARIADB_USER=matomo
		MARIADB_PASSWORD=$(openssl rand -hex 16)
	EOF
}

sql() {
	compose exec -T db sh -c 'exec mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE" -N -e "$0"' "$1"
}

# Drives the web installer: Matomo has no console installer.
seed() {
	local jar=$work/cookies p=$base/index.php password
	local post=(curl -fs -o /dev/null -c "$jar" -b "$jar")
	password=$(openssl rand -hex 16)
	"${post[@]}" "$p?action=systemCheck&module=Installation"
	"${post[@]}" -d _qf__databasesetupform= -d host=db -d username=matomo \
		-d password="$(grep MARIADB_PASSWORD= "$kit/.env" | cut -d= -f2)" -d dbname=matomo \
		-d tables_prefix=matomo_ --data-urlencode 'adapter=PDO\MYSQL' \
		-d schema=Mariadb -d submit=Next "$p?action=databaseSetup&module=Installation"
	"${post[@]}" "$p?action=tablesCreation&module=Installation"
	"${post[@]}" -d _qf__generalsetupform= -d login=rescue -d password="$password" \
		-d password_bis="$password" -d email=ci@example.org -d submit=Next \
		"$p?action=setupSuperUser&module=Installation"
	"${post[@]}" -d _qf__websitesetupform= -d siteName="$SITE_NAME" \
		-d url=https://example.org -d timezone=Europe/Paris -d ecommerce=0 \
		-d submit=Next "$p?action=firstWebsiteSetup&module=Installation"
	"${post[@]}" -d _qf__defaultsettingsform= -d setup_geoip2=0 -d anonymise_ip=1 \
		-d submit=Continue "$p?action=finished&module=Installation"
	rm -f "$jar"

	curl -fs -o /dev/null "$base/matomo.php?idsite=1&rec=1&url=https://example.org/&action_name=rescue-ci"
	curl -fsSL -o "$work/plugin.zip" \
		"https://plugins.matomo.org/api/2.0/plugins/$PLUGIN/download/latest?matomo=$(version)"
	as_root 'unzip -q /work/plugin.zip -d /kit/volumes/plugins && chown -R 33:33 /kit/volumes/plugins'
	rm "$work/plugin.zip"
	compose exec -T -u www-data app php console plugin:activate "$PLUGIN"
}

check() {
	[ "$(sql 'select name from matomo_site where idsite = 1')" = "$SITE_NAME" ]
	[ "$(sql 'select count(*) from matomo_log_visit')" = 1 ]
	compose exec -T app php console plugin:list | grep -E "\b$PLUGIN\b.*Activated" >/dev/null
	[ "$(curl -fs "$base/misc/user/logo.png" | base64 -w0)" = "$LOGO_B64" ]
	curl -fs "$base/index.php" | grep -c 'Sign in' >/dev/null
	echo "site, visit, $PLUGIN, logo and login page are there"
}
