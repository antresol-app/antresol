package addons

import (
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validJSON() string {
	return `{"id":"demo-addon","name":{"ru":"Демо","en":"Demo"},"description":{"ru":"Описание","en":"Desc"},
"version":"1.2.3","author":"A","homepage":"https://example.org","contract":1,
"platforms":{"linux/amd64":{"asset":"demo-linux-amd64","sha256":"` + sha + `"},
"linux/arm64":{"asset":"demo-linux-arm64","sha256":"` + sha + `"}},
"web":{"asset":"web.tar.gz","sha256":"` + sha + `"},
"permissions":["library.read","events"]}`
}

func TestParseManifest_OK(t *testing.T) {
	m, err := ParseManifest([]byte(validJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "demo-addon" || m.Version != "1.2.3" || m.Name.EN != "Demo" || m.Web == nil ||
		m.Web.Asset != "web.tar.gz" || len(m.Platforms) != 2 || len(m.Permissions) != 2 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestParseManifest_Rejects(t *testing.T) {
	const (
		badID    = "неверный id"
		reserved = "зарезервирован"
	)
	withID := func(id string) string {
		return strings.Replace(validJSON(), `"demo-addon"`, `"`+id+`"`, 1)
	}
	rep := func(old, new string) string { return strings.Replace(validJSON(), old, new, 1) }
	cases := []struct{ name, in, want string }{
		{"bad id", withID("Bad_Id"), badID},
		{"long id", withID(strings.Repeat("a", 41)), badID},
		{"digit-leading id", withID("1abc"), badID},
		{"one-char id", withID("a"), badID},
		{"reserved web", withID("web"), reserved},
		{"reserved preview", withID("preview"), reserved},
		{"reserved x", withID("x"), reserved},
		{"reserved ui", withID("ui"), reserved},
		{"reserved run", withID("run"), reserved},
		{"reserved addon", withID("addon"), reserved},
		{"reserved core", withID("core"), reserved},
		{"reserved contract", withID("contract"), reserved},
		{"reserved locale", withID("locale"), reserved},
		{"reserved addon- prefix", withID("addon-x"), reserved},
		{"reserved core- prefix", withID("core-y"), reserved},
		{"bad access", rep(`"permissions"`, `"access":"everyone","permissions"`), "неверный доступ"},
		{"unknown hook", rep(`"permissions"`, `"hooks":["files.nope"],"permissions"`), "неизвестный хук"},
		{"empty scan kind", rep(`"permissions"`, `"hooks":["tracking.scan:"],"permissions"`), "неизвестный хук"},
		{"dropped import.public", rep(`"permissions"`, `"hooks":["import.public"],"permissions"`), "неизвестный хук"},
		{"duplicate hook", rep(`"permissions"`, `"hooks":["files.plan","files.plan"],"permissions"`), "повторяется хук"},
		{"bad topic", rep(`"permissions"`, `"notify":[{"topic":"Bad","title":{"ru":"т","en":"t"},"audience":"admins"}],"permissions"`), "неверная тема"},
		{"duplicate topic", rep(`"permissions"`, `"notify":[{"topic":"a","title":{"ru":"т","en":"t"},"audience":"admins"},{"topic":"a","title":{"ru":"т","en":"t"},"audience":"adults"}],"permissions"`), "повторяется тема"},
		{"bad audience", rep(`"permissions"`, `"notify":[{"topic":"a","title":{"ru":"т","en":"t"},"audience":"kids"}],"permissions"`), "неверная аудитория"},
		{"trailing brace", validJSON() + "}", "прочитать JSON"},
		{"trailing value", validJSON() + " 1", "прочитать JSON"},
		{"unknown platform", rep(`"linux/arm64"`, `"windows/amd64"`), "неизвестная платформа"},
		{"platform garbage", rep(`"linux/arm64"`, `"linux"`), "неизвестная платформа"},
		{"no name", rep(`"name":{"ru":"Демо","en":"Demo"}`, `"name":{"ru":"","en":" "}`), "нет названия"},
		{"duplicate permission", rep(`["library.read","events"]`, `["events","events"]`), "повторяется разрешение"},
		{"v version", rep(`"1.2.3"`, `"v1.0.0"`), "неверная версия"},
		{"contract", rep(`"contract":1`, `"contract":2`), "версия контракта"},
		{"permission", rep(`"events"`, `"files.write"`), "неизвестное разрешение"},
		{"bad sha", strings.ReplaceAll(validJSON(), sha, "xyz"), "контрольная сумма"},
		{"asset slash", rep(`"web.tar.gz"`, `"a/b.tar.gz"`), "имя файла"},
		{"unknown field", rep(`"author"`, `"extra":1,"author"`), "прочитать JSON"},
		{"not json", `{`, "прочитать JSON"},
		{"nothing", `{"id":"demo-addon","name":{"ru":"Д","en":"D"},"version":"1.0.0","contract":1,"platforms":{},"permissions":[]}`, "нет ни платформ"},
	}
	for _, c := range cases {
		_, err := ParseManifest([]byte(c.in))
		if err == nil {
			t.Errorf("%s: accepted", c.name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q lacks %q", c.name, err, c.want)
		}
	}
}

func TestPlatformFor(t *testing.T) {
	m, err := ParseManifest([]byte(validJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := m.PlatformFor("linux", "arm64"); !ok || p.Asset != "demo-linux-arm64" {
		t.Fatalf("linux/arm64: %+v %v", p, ok)
	}
	if _, ok := m.PlatformFor("windows", "amd64"); ok {
		t.Fatal("windows/amd64 found")
	}
}

func TestParseManifest_NameInOneLanguage(t *testing.T) {
	in := strings.Replace(validJSON(), `"name":{"ru":"Демо","en":"Demo"}`, `"name":{"ru":"","en":"Demo"}`, 1)
	if _, err := ParseManifest([]byte(in)); err != nil {
		t.Fatalf("english-only name rejected: %v", err)
	}
	in = strings.Replace(validJSON(), `"linux/arm64"`, `"darwin/arm64"`, 1)
	if _, err := ParseManifest([]byte(in)); err != nil {
		t.Fatalf("darwin/arm64 rejected: %v", err)
	}
}

func TestParseManifest_Stage2Fields(t *testing.T) {
	in := strings.Replace(validJSON(), `"permissions":["library.read","events"]`,
		`"access":"adults","hooks":["files.plan","tracking.scan:pinned","requests.search","library.afterScan","home.continue"],
"notify":[{"topic":"done","title":{"ru":"Готово","en":"Done"},"audience":"adults"}],
"permissions":["library.read","events","library.write","files.holds","metadata","images","play","notify","users.read","tracking","imports","requests","voiceover","progress"]`, 1)
	m, err := ParseManifest([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if m.Access != "adults" || !m.ForAdults() {
		t.Errorf("access %q", m.Access)
	}
	if !m.Answers("files.plan") || !m.Answers("tracking.scan:pinned") || !m.Answers("requests.search") || !m.Answers("library.afterScan") || !m.Answers("home.continue") || m.Answers("files.stop") {
		t.Error("Answers wrong")
	}
	if len(m.Notify) != 1 || m.Notify[0].Topic != "done" || m.Notify[0].Title.EN != "Done" || m.Notify[0].Audience != "adults" {
		t.Errorf("notify %+v", m.Notify)
	}
	base, err := ParseManifest([]byte(validJSON()))
	if err != nil || base.ForAdults() {
		t.Errorf("default access must be admins: %v", err)
	}
	for _, a := range []string{"admins", ""} {
		if (Manifest{Access: a}).ForAdults() {
			t.Errorf("%q is not adults", a)
		}
	}
}

// The four channels.* hooks come together: a manifest with some of them
// only is refused.
func TestManifest_ChannelsHooksAllOrNone(t *testing.T) {
	with := func(hooks string) string {
		return strings.Replace(validJSON(), `"permissions"`, `"hooks":[`+hooks+`],"permissions"`, 1)
	}
	all := `"channels.ready","channels.candidates","channels.resolve","channels.release"`
	m, err := ParseManifest([]byte(with(all)))
	if err != nil || !m.Answers("channels.resolve") {
		t.Fatalf("all four: %v", err)
	}
	for _, some := range []string{`"channels.ready"`, `"channels.ready","channels.candidates","channels.resolve"`,
		`"channels.release","files.plan"`} {
		_, err := ParseManifest([]byte(with(some)))
		if err == nil || err.Error() != "Нужны все хуки channels.*" {
			t.Errorf("%s: %v", some, err)
		}
	}
	if _, err := ParseManifest([]byte(with(`"files.plan"`))); err != nil {
		t.Fatalf("none of them: %v", err)
	}
}
