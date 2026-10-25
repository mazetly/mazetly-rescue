package compose

import (
	"strings"
	"testing"
)

const config = `name: kit
services:
  app:
    image: matomo:5-apache
    volumes:
      - type: bind
        source: /srv/kit/volumes/data
        target: /app/data
    x-mazetly:
      database:
        engine: sqlite
        path: /app/data/app.db
      hooks:
        post_restore:
          - php console core:update --yes
          - user: www-data
            run: php console core:archive
  db:
    image: mariadb:11.4
    x-mazetly:
      database:
        engine: mariadb
      hooks:
        post_restore:
          - echo db
x-mazetly:
  app: demo
  version:
    service: app
    label: org.opencontainers.image.version
`

func TestParse(t *testing.T) {
	p, err := Parse("/srv/kit", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if p.App != "demo" || p.Version.Label != "org.opencontainers.image.version" {
		t.Errorf("app %q, version %+v", p.App, p.Version)
	}
	dbs := p.Databases()
	if len(dbs) != 2 || dbs[0].Service != "app" || dbs[1].Engine != "mariadb" {
		t.Fatalf("databases %+v", dbs)
	}
	if dbs[0].File() != "db/app.sqlite" || dbs[1].File() != "db/db.sql" {
		t.Errorf("files %s, %s", dbs[0].File(), dbs[1].File())
	}
}

func TestHooksInServiceOrder(t *testing.T) {
	p, err := Parse("/srv/kit", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range p.Hooks("post_restore") {
		got = append(got, h.Service+"/"+h.User+"/"+h.Run)
	}
	want := "app//php console core:update --yes,app/www-data/php console core:archive,db//echo db"
	if strings.Join(got, ",") != want {
		t.Errorf("hooks %v", got)
	}
}

func TestHostPath(t *testing.T) {
	p, err := Parse("/srv/kit", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.HostPath("app", "/app/data/app.db"); got != "/srv/kit/volumes/data/app.db" {
		t.Errorf("host path %q", got)
	}
	if _, err := p.HostPath("app", "/app/database.db"); err == nil {
		t.Error("a path next to the mount, not under it, must not match")
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"no app":         strings.Replace(config, "app: demo", "", 1),
		"unknown engine": strings.Replace(config, "engine: mariadb", "engine: oracle", 1),
		"unknown phase":  strings.Replace(config, "post_restore:\n          - echo", "after_restore:\n          - echo", 1),
		"sqlite unbound": strings.Replace(config, "path: /app/data/app.db", "path: /elsewhere/app.db", 1),
		"bad version":    strings.Replace(config, "label: org", "run: x\n    label: org", 1),
	}
	for name, c := range cases {
		if _, err := Parse("/srv/kit", []byte(c)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
