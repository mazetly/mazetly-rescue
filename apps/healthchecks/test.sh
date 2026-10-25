# Healthchecks round-trip test: sourced by test/roundtrip.sh.

READY_PATH=/accounts/login/
# Backed up on the previous major release, restored as is, then upgraded.
BACKUP_IMAGES=(web=healthchecks/healthchecks:v4.4)
UPGRADE=(web=healthchecks/healthchecks:v5.0)

prepare() {
	cat >"$kit/.env" <<-EOF
		POSTGRES_DB=hc
		POSTGRES_USER=hc
		POSTGRES_PASSWORD=$(openssl rand -hex 16)
		SECRET_KEY=$(openssl rand -hex 32)
		SITE_ROOT=$base
		ALLOWED_HOSTS=*
		RESCUE_TEST_PASSWORD=$(openssl rand -hex 16)
	EOF
}

django() { compose exec -T web ./manage.py shell -v 0 -c "$1" | tr -d '\r'; }

seed() {
	django '
import os
from django.contrib.auth.models import User
from hc.accounts.models import Profile, Project
from hc.api.models import Check
user = User.objects.create_user("rescue", "ci@example.org", os.environ["RESCUE_TEST_PASSWORD"])
Profile.objects.for_user(user)
project = Project.objects.create(owner=user, name="Rescue CI")
Check.objects.create(project=project, name="Rescue CI")'
	[ "$(curl -fs "$base/ping/$(code)")" = OK ]
}

code() { django 'from hc.api.models import Check; print(Check.objects.get(name="Rescue CI").code)'; }

check() {
	# The check's ping URL is what monitored jobs call: it must keep working.
	[ "$(curl -fs "$base/ping/$(code)")" = OK ]
	[ "$(django '
import os
from django.contrib.auth.models import User
from hc.api.models import Check
print(User.objects.get(username="rescue").check_password(os.environ["RESCUE_TEST_PASSWORD"]),
      Check.objects.get(name="Rescue CI").n_pings >= 1)')" = "True True" ]
	echo "user, check, its ping URL and its ping history are there"
}
