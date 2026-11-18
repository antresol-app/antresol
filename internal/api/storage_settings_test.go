package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mediacheck"
)

// storageEnv is a server whose media folder is a temp dir with a library
// laid out the usual way, except that «series» is called «tv».
type storageEnv struct {
	*libEnv
	root     string
	restarts chan struct{}
	jobs     *library.Jobs
	tr       string
}

// storageOpts changes what newStorageEnvWith builds.
type storageOpts struct {
	root, transcode string // "": a temp dir, and tmp/antresol-hls in it
	bare            bool   // nothing laid out under the media folder
	env             *config.Folders
	offline         func() string // StorageInfo.Offline
}

func newStorageEnv(t *testing.T) *storageEnv { return newStorageEnvWith(t, storageOpts{}) }

func newStorageEnvWith(t *testing.T, o storageOpts) *storageEnv {
	t.Helper()
	restartDelay = 0
	dockerEnv = func() bool { return true }
	t.Cleanup(func() {
		restartDelay = 300 * time.Millisecond
		dockerEnv = func() bool { _, err := os.Stat("/.dockerenv"); return err == nil }
	})
	root := o.root
	if root == "" {
		root = t.TempDir()
	}
	if !o.bare {
		for _, f := range []string{"movies/A (2001)/A (2001).mkv", "movies/B (2002)/B (2002).mkv", "tv/S/S s01e01.mkv", "music/Band/x.flac", "books/ebooks/x.epub"} {
			p := filepath.Join(root, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	tr := o.transcode
	if tr == "" {
		tr = filepath.Join(root, "tmp", "antresol-hls")
	}
	env := config.Folders{Movies: "movies", Series: "series", Music: "music", Ebooks: "books/ebooks",
		Audiobooks: "books/audiobooks", Comics: "books/manga"}
	if o.env != nil {
		env = *o.env
	}
	s := &storageEnv{root: root, restarts: make(chan struct{}, 4), jobs: &library.Jobs{}, tr: tr}
	s.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.MediaRoot = root
		d.Jobs = s.jobs
		d.Storage = &StorageInfo{
			Env:          env,
			TranscodeDir: s.tr, ConfigDir: "/config", Service: "antresol",
			Offline: o.offline,
			Restart: func() { s.restarts <- struct{}{} },
		}
	})
	return s
}

func (s *storageEnv) view(w *bytes.Buffer) storageView {
	var v storageView
	if err := json.Unmarshal(w.Bytes(), &v); err != nil {
		s.t.Fatal(err)
	}
	return v
}

func rowOf(v storageView, kind string) folderRow {
	for _, r := range v.Folders {
		if r.Kind == kind {
			return r
		}
	}
	return folderRow{}
}

func TestStorageViewShowsWhatTheServerSees(t *testing.T) {
	s := newStorageEnv(t)
	w := s.do("GET", "/api/settings/storage", "", s.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	v := s.view(w.Body)
	if v.Root.Status != mediacheck.StatusOK || v.Root.Path != s.root {
		t.Fatalf("root %+v", v.Root)
	}
	names := map[string]int{}
	for _, e := range v.Entries {
		names[e.Name] = e.Items
	}
	if names["movies"] != 2 || names["tv"] != 1 || names["music"] != 1 || names["books/ebooks"] != 1 {
		t.Fatalf("entries %v", names)
	}
	if r := rowOf(v, "movies"); r.Status != mediacheck.StatusOK || r.Items != 2 || r.Path != "movies" || r.Default != "movies" {
		t.Fatalf("movies %+v", r)
	}
	// «series» is not there but «tv» looks like it
	if r := rowOf(v, "series"); r.Status != mediacheck.StatusMissing || r.Suggest != "tv" {
		t.Fatalf("series %+v", r)
	}
	// no suggestion for a folder that is just not there
	if r := rowOf(v, "comics"); r.Status != mediacheck.StatusMissing || r.Suggest != "" {
		t.Fatalf("comics %+v", r)
	}
	if v.Transcode.Status != mediacheck.StatusOK {
		t.Fatalf("transcode %+v", v.Transcode)
	}
	if st, err := os.Stat(s.tr); err != nil || !st.IsDir() {
		t.Fatal("the transcode folder was not made")
	}
	if v.Server.UID != os.Getuid() || v.Server.GID != os.Getgid() || v.Custom || v.Fix != nil || !v.Restarts {
		t.Fatalf("%+v", v)
	}
	// looking never writes into the library folders
	if ents, _ := os.ReadDir(filepath.Join(s.root, "movies")); len(ents) != 2 {
		t.Fatalf("%v", ents)
	}
}

func TestStorageCheckOfPickedFolders(t *testing.T) {
	s := newStorageEnv(t)
	w := s.do("POST", "/api/settings/storage/check", `{"folders":{"series":"tv","comics":"books/manga"}}`, s.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	v := s.view(w.Body)
	if r := rowOf(v, "series"); r.Status != mediacheck.StatusOK || r.Path != "tv" || r.Items != 1 || r.Default != "series" {
		t.Fatalf("%+v", r)
	}
	// a kind left out keeps the saved one
	if r := rowOf(v, "movies"); r.Path != "movies" {
		t.Fatalf("%+v", r)
	}
	// nothing was saved
	if _, custom, _ := LoadFolderSettings(context.Background(), s.db); custom {
		t.Fatal("a check saved the folders")
	}
	// without a body: the saved ones
	if w := s.do("POST", "/api/settings/storage/check", "", s.admin); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}

func TestStorageNotWritableFolderComesWithTheFix(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	s := newStorageEnv(t)
	music := filepath.Join(s.root, "music")
	if err := os.Chmod(music, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(music, 0o755)
	v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	r := rowOf(v, "music")
	if r.Status != mediacheck.StatusReadOnly || r.Why != mediacheck.WhyDenied || r.Error == "" {
		t.Fatalf("%+v", r)
	}
	if rowOf(v, "movies").Status != mediacheck.StatusOK {
		t.Fatal("a writable folder was flagged")
	}
	if v.Fix == nil || len(v.Fix.Paths) != 1 || v.Fix.Paths[0] != music || !v.Fix.Docker ||
		!strings.HasPrefix(v.Fix.Chown, "docker compose run --rm --no-deps --user root --entrypoint chown antresol -R ") ||
		!strings.HasSuffix(v.Fix.Chown, " "+music) || v.Fix.UID != os.Getuid() {
		t.Fatalf("%+v", v.Fix)
	}
	// the owner is the server's own user: the mode bits are at fault, no `user:` advice
	if v.Fix.ComposeUser != "" {
		t.Fatalf("%+v", v.Fix)
	}
}

func TestStorageSaveRestartsOnlyWhenSomethingChanged(t *testing.T) {
	s := newStorageEnv(t)
	ctx := context.Background()
	w := s.do("PUT", "/api/settings/storage", `{"folders":{"series":"tv"}}`, s.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out storagePut
	json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Restarting || !out.Custom || rowOf(out.storageView, "series").Path != "tv" || rowOf(out.storageView, "series").Status != mediacheck.StatusOK {
		t.Fatalf("%+v", out)
	}
	select {
	case <-s.restarts:
	case <-time.After(2 * time.Second):
		t.Fatal("the server was not asked to restart")
	}
	f, custom, err := LoadFolderSettings(ctx, s.db)
	if err != nil || !custom || f.Series != "tv" || f.Movies != "movies" || f.Comics != "books/manga" {
		t.Fatalf("%+v %v %v", f, custom, err)
	}
	// the same again: nothing to restart for
	w = s.do("PUT", "/api/settings/storage", `{"folders":{"series":"tv"}}`, s.admin)
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out.Restarting {
		t.Fatalf("%d %+v", w.Code, out)
	}
	select {
	case <-s.restarts:
		t.Fatal("restarted for nothing")
	case <-time.After(100 * time.Millisecond):
	}
	// back to the environment's: the row is dropped
	w = s.do("PUT", "/api/settings/storage", `{"reset":true}`, s.admin)
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || !out.Restarting || out.Custom || rowOf(out.storageView, "series").Path != "series" {
		t.Fatalf("%d %+v", w.Code, out)
	}
	<-s.restarts
	if _, custom, _ := LoadFolderSettings(ctx, s.db); custom {
		t.Fatal("the row stayed")
	}
	// choosing the environment's own folders by hand is no change either
	s.do("PUT", "/api/settings/storage", `{"folders":{"series":"tv"}}`, s.admin)
	<-s.restarts
	w = s.do("PUT", "/api/settings/storage", `{"folders":{"series":"series"}}`, s.admin)
	json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Restarting || out.Custom {
		t.Fatalf("%+v", out)
	}
	<-s.restarts
}

func TestStorageSaveWithoutARestarter(t *testing.T) {
	s := newStorageEnv(t)
	// a server that cannot restart itself says so and still keeps the choice
	r := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB, d.MediaRoot = db, s.root
		d.Storage = &StorageInfo{Env: config.Folders{Movies: "movies", Series: "series", Music: "music", Ebooks: "books/ebooks",
			Audiobooks: "books/audiobooks", Comics: "books/manga"}}
	})
	w := r.do("PUT", "/api/settings/storage", `{"folders":{"series":"tv"}}`, r.admin)
	var out storagePut
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out.Restarting || out.Restarts || !out.Custom {
		t.Fatalf("%d %+v", w.Code, out)
	}
}

func TestStorageRefusesBadFolders(t *testing.T) {
	s := newStorageEnv(t)
	for name, body := range map[string]string{
		"outside":   `{"folders":{"movies":"../elsewhere"}}`,
		"absolute":  `{"folders":{"movies":"/etc"}}`,
		"the root":  `{"folders":{"movies":"."}}`,
		"root abs":  `{"folders":{"movies":"` + s.root + `"}}`,
		"same twin": `{"folders":{"movies":"tv","series":"tv"}}`,
		"nested up": `{"folders":{"music":"music/../.."}}`,
	} {
		for _, path := range []string{"/api/settings/storage", "/api/settings/storage/check"} {
			method := "PUT"
			if strings.HasSuffix(path, "check") {
				method = "POST"
			}
			w := s.do(method, path, body, s.admin)
			// a clash of two kinds is only a problem when saving
			if name == "same twin" && method == "POST" {
				continue
			}
			if w.Code != 400 {
				t.Errorf("%s %s: %d %s", name, method, w.Code, w.Body)
			}
		}
	}
	if _, custom, _ := LoadFolderSettings(context.Background(), s.db); custom {
		t.Fatal("a refused request saved something")
	}
	select {
	case <-s.restarts:
		t.Fatal("restarted for a refused request")
	case <-time.After(50 * time.Millisecond):
	}
	// an absolute path inside the media folder is the same folder as its relative one
	w := s.do("POST", "/api/settings/storage/check", `{"folders":{"series":"`+filepath.Join(s.root, "tv")+`"}}`, s.admin)
	if v := s.view(w.Body); w.Code != 200 || rowOf(v, "series").Path != "tv" {
		t.Fatalf("%d %+v", w.Code, rowOf(v, "series"))
	}
}

func TestStorageCreateFolder(t *testing.T) {
	s := newStorageEnv(t)
	w := s.do("POST", "/api/settings/storage/create", `{"path":"books/manga"}`, s.admin)
	var d mediacheck.Dir
	json.Unmarshal(w.Body.Bytes(), &d)
	if w.Code != 200 || d.Status != mediacheck.StatusOK || d.Path != "books/manga" {
		t.Fatalf("%d %+v", w.Code, d)
	}
	if st, err := os.Stat(filepath.Join(s.root, "books", "manga")); err != nil || !st.IsDir() {
		t.Fatal("not made")
	}
	for _, body := range []string{`{"path":"../out"}`, `{"path":"/tmp/x"}`, `{"path":""}`, `{"path":"."}`} {
		if w := s.do("POST", "/api/settings/storage/create", body, s.admin); w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.root), "out")); err == nil {
		t.Fatal("made a folder outside the media folder")
	}
	// an unwritable parent comes back as a problem to show, not a crash
	if os.Geteuid() != 0 {
		ro := filepath.Join(s.root, "ro")
		os.Mkdir(ro, 0o555)
		defer os.Chmod(ro, 0o755)
		w := s.do("POST", "/api/settings/storage/create", `{"path":"ro/new"}`, s.admin)
		json.Unmarshal(w.Body.Bytes(), &d)
		if w.Code != 200 || d.Status != mediacheck.StatusReadOnly || d.Why != mediacheck.WhyDenied {
			t.Fatalf("%d %+v", w.Code, d)
		}
	}
}

func TestStorageAdminsOnly(t *testing.T) {
	s := newStorageEnv(t)
	kid := s.kid("")
	for _, c := range [][2]string{{"GET", "/api/settings/storage"}, {"POST", "/api/settings/storage/check"},
		{"PUT", "/api/settings/storage"}, {"POST", "/api/settings/storage/create"},
		{"GET", "/api/setup/wizard"}, {"POST", "/api/setup/wizard/finish"}} {
		if w := s.do(c[0], c[1], `{"path":"x","folders":{"series":"tv"}}`, kid); w.Code != 403 {
			t.Errorf("kid %s %s: %d", c[0], c[1], w.Code)
		}
		if w := s.do(c[0], c[1], `{}`, nil); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", c[0], c[1], w.Code)
		}
	}
	if _, custom, _ := LoadFolderSettings(context.Background(), s.db); custom {
		t.Fatal("a kid saved folders")
	}
}

func TestSetupWizardIsPendingOnlyOnceAndFinishScans(t *testing.T) {
	s := newStorageEnv(t)
	var got struct{ Pending bool }
	// the administrator was just created by newLibEnvWith
	w := s.do("GET", "/api/setup/wizard", "", s.admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || !got.Pending {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if s.jobs.Status().Queued {
		t.Fatal("a scan was queued before the end")
	}
	if w := s.do("POST", "/api/setup/wizard/finish", "", s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !s.jobs.Status().Queued {
		t.Fatal("finishing did not start a library scan")
	}
	got.Pending = true
	w = s.do("GET", "/api/setup/wizard", "", s.admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Pending {
		t.Fatal("still pending")
	}
	// over: the steps are no longer reachable
	if w := s.do("POST", "/api/setup/wizard/finish", "", s.admin); w.Code != 403 {
		t.Fatalf("a finished wizard finished again: %d", w.Code)
	}
	// the same checks stay in the settings
	if w := s.do("GET", "/api/settings/storage", "", s.admin); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}

func TestSetupWizardNotPendingOnAServerSetUpBefore(t *testing.T) {
	s := newStorageEnv(t)
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, wizardKey); err != nil {
		t.Fatal(err)
	}
	var got struct{ Pending bool }
	w := s.do("GET", "/api/setup/wizard", "", s.admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Pending {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := s.do("POST", "/api/setup/wizard/finish", "", s.admin); w.Code != 403 {
		t.Fatalf("%d", w.Code)
	}
}

func TestSetupWizardNotMarkedWithoutStorage(t *testing.T) {
	// a server without the folder checks has no wizard to wait for
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	var n int
	e.db.QueryRow(`SELECT count(*) FROM settings WHERE key=?`, wizardKey).Scan(&n)
	if n != 0 {
		t.Fatal("pending without a wizard")
	}
}

// indexed puts a movie file under the media folder into the library, as a
// scan does, so the library has files.
func (s *storageEnv) indexed(rel string) {
	s.t.Helper()
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	if _, err := s.repo.UpsertMovie(context.Background(), library.MovieInput{Title: rel, Year: 2001,
		File: library.FileInput{Path: p, Size: 1}}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *storageEnv) mk(rel ...string) {
	s.t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(s.root, filepath.FromSlash(r)), 0o755); err != nil {
			s.t.Fatal(err)
		}
	}
}

func (s *storageEnv) put(rel, content string) {
	s.t.Helper()
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func TestStorageAMissingMediaFolderGetsNoChownAndIsNotMade(t *testing.T) {
	// the compose file has no volume for the media folder: it is not there,
	// and the transcode folder inside it must not make it (which would
	// also hide the problem), nor must the fix say chown of / or /media's parent
	root := filepath.Join(t.TempDir(), "media")
	s := newStorageEnvWith(t, storageOpts{root: root, bare: true})
	w := s.do("GET", "/api/settings/storage", "", s.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	v := s.view(w.Body)
	if v.Root.Status != mediacheck.StatusMissing || v.Transcode.Status != mediacheck.StatusMissing {
		t.Fatalf("root %+v transcode %+v", v.Root, v.Transcode)
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("looking made the media folder")
	}
	if strings.Contains(w.Body.String(), "chown") || (v.Fix != nil && (v.Fix.Chown != "" || len(v.Fix.Paths) != 0)) {
		t.Fatalf("advice to hand over a folder: %+v", v.Fix)
	}
	if v.CanCreate {
		t.Fatal("«Создать» offered with no media folder")
	}
}

func TestStorageNeverAdvisesAChownAboveTheFoldersItOwns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	// a transcode folder in a place the server cannot make: the closest
	// folder that exists is somebody else's mount, not the server's to take
	outside := t.TempDir()
	mnt := filepath.Join(outside, "mnt")
	if err := os.Mkdir(mnt, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(mnt, 0o755)
	tr := filepath.Join(mnt, "scratch", "hls")
	s := newStorageEnvWith(t, storageOpts{transcode: tr})
	w := s.do("GET", "/api/settings/storage", "", s.admin)
	v := s.view(w.Body)
	if v.Transcode.Status != mediacheck.StatusReadOnly {
		t.Fatalf("%+v", v.Transcode)
	}
	if v.Fix == nil || v.Fix.Chown != "" || len(v.Fix.Paths) != 0 || len(v.Fix.Mount) != 1 || v.Fix.Mount[0] != tr {
		t.Fatalf("%+v", v.Fix)
	}
	if strings.Contains(w.Body.String(), "chown") {
		t.Fatalf("a chown in the answer: %s", w.Body)
	}
	// the page reads fix.paths.length: it is a list even when empty
	if !strings.Contains(w.Body.String(), `"paths":[]`) {
		t.Fatalf("fix.paths is not an empty list: %s", w.Body)
	}
}

func TestStorageChownAdviceStopsAtTheMediaFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	s := newStorageEnv(t)
	// the media folder is read-only for the server: the transcode folder in it
	// is not made, and the one fix is the media folder itself (never its parent)
	if err := os.RemoveAll(s.tr); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Dir(s.tr))
	if err := os.Chmod(s.root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.root, 0o755)
	v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	if v.Fix == nil || len(v.Fix.Paths) != 1 || v.Fix.Paths[0] != s.root || len(v.Fix.Mount) != 0 ||
		!strings.HasSuffix(v.Fix.Chown, " "+s.root) {
		t.Fatalf("%+v", v.Fix)
	}
}

func TestStorageCreateRefusedWhileTheDiskLooksUnmounted(t *testing.T) {
	s := newStorageEnv(t)
	// a library that has files, on a disk that is there: «Создать» works for
	// a folder the scanner would make too
	s.indexed("movies/A (2001)/A (2001).mkv")
	if v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body); v.Offline || !v.CanCreate {
		t.Fatalf("an online disk: offline %v canCreate %v", v.Offline, v.CanCreate)
	}
	if w := s.do("POST", "/api/settings/storage/create", `{"path":"books/manga"}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// the disk goes away: the mount point is empty, the folders are gone
	for _, d := range []string{"movies", "tv", "music", "books", "tmp"} {
		if err := os.RemoveAll(filepath.Join(s.root, d)); err != nil {
			t.Fatal(err)
		}
	}
	w := s.do("GET", "/api/settings/storage", "", s.admin)
	v := s.view(w.Body)
	if !v.Offline || v.CanCreate || rowOf(v, "movies").Status != mediacheck.StatusMissing {
		t.Fatalf("offline %v canCreate %v movies %+v", v.Offline, v.CanCreate, rowOf(v, "movies"))
	}
	// looking did not make the transcode folder on the disk under the mount
	// point: that would stop it looking empty
	if ents, _ := os.ReadDir(s.root); len(ents) != 0 {
		t.Fatalf("looking put %d entries on the disk that is not there", len(ents))
	}
	for _, p := range []string{"movies", "series", "books/manga", "somewhere/new"} {
		w := s.do("POST", "/api/settings/storage/create", `{"path":"`+p+`"}`, s.admin)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Диск не подключён") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
		if _, err := os.Stat(filepath.Join(s.root, p)); err == nil {
			t.Errorf("%s was made on the disk that is not there", p)
		}
	}
}

func TestStorageCreateRefusedWhenTheScannersFoldersAreGone(t *testing.T) {
	// the media folder holds something (a stray folder on the disk under the
	// mount), but the movies and series folders the scanner watches are
	// not there: that is the scanner's own sign of an unmounted disk
	root := t.TempDir()
	s := newStorageEnvWith(t, storageOpts{root: root, bare: true, offline: func() string {
		for _, d := range []string{"movies", "tv"} {
			if st, err := os.Stat(filepath.Join(root, d)); err != nil || !st.IsDir() {
				return "no " + d
			}
		}
		return ""
	}})
	s.mk("tmp")
	s.indexed("movies/A (2001)/A (2001).mkv")
	w := s.do("POST", "/api/settings/storage/create", `{"path":"movies"}`, s.admin)
	if w.Code != 409 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "movies")); err == nil {
		t.Fatal("movies was made on the disk that is not there")
	}
}

func TestStorageCreateOnAFreshInstallStillWorks(t *testing.T) {
	// nothing is indexed, so nothing can be hidden: an empty media folder is a
	// new install, and its folders are made
	s := newStorageEnvWith(t, storageOpts{bare: true})
	v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	if v.Offline || !v.CanCreate {
		t.Fatalf("offline %v canCreate %v", v.Offline, v.CanCreate)
	}
	if w := s.do("POST", "/api/settings/storage/create", `{"path":"movies"}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestStorageCreateNeverMakesTheMediaFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	s := newStorageEnvWith(t, storageOpts{root: root, bare: true})
	w := s.do("POST", "/api/settings/storage/create", `{"path":"movies"}`, s.admin)
	if w.Code != 409 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("made the media folder")
	}
}

func TestStorageSuggestsTheRealFolderBesideTheEmptyDefaults(t *testing.T) {
	// a new install: the first scan made the empty default folders, and the
	// real ones are called Фильмы and Сериалы
	s := newStorageEnvWith(t, storageOpts{bare: true})
	s.mk("movies", "series", "music", "books/ebooks", "books/audiobooks", "books/manga")
	for i := 0; i < 3; i++ {
		s.put("Фильмы/f"+string(rune('a'+i))+"/f.mkv", "x")
	}
	s.put("Сериалы/S/e.mkv", "x")
	v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	m, se := rowOf(v, "movies"), rowOf(v, "series")
	if m.Status != mediacheck.StatusOK || m.Items != 0 || m.Suggest != "Фильмы" {
		t.Fatalf("movies %+v", m)
	}
	if se.Status != mediacheck.StatusOK || se.Items != 0 || se.Suggest != "Сериалы" {
		t.Fatalf("series %+v", se)
	}
	// an empty folder with no better look-alike: nothing to offer
	if r := rowOf(v, "music"); r.Suggest != "" {
		t.Fatalf("music %+v", r)
	}
	// picking the offered folder: it is found, with files, and nothing more to offer
	w := s.do("POST", "/api/settings/storage/check", `{"folders":{"movies":"Фильмы"}}`, s.admin)
	v = s.view(w.Body)
	if m := rowOf(v, "movies"); m.Path != "Фильмы" || m.Items != 3 || m.Suggest != "" {
		t.Fatalf("%+v", m)
	}
	// a folder with files is never second-guessed
	s.put("movies/keep/k.mkv", "x")
	v = s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	if m := rowOf(v, "movies"); m.Items != 1 || m.Suggest != "" {
		t.Fatalf("%+v", m)
	}
}

func TestStorageRefusesLinksThatLeaveTheMediaFolder(t *testing.T) {
	s := newStorageEnv(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.root, "escape")); err != nil {
		t.Fatal(err)
	}
	// a link that stays inside is an ordinary folder
	if err := os.Symlink(filepath.Join(s.root, "tv"), filepath.Join(s.root, "alias")); err != nil {
		t.Fatal(err)
	}
	// create through the link, and the link itself
	for _, p := range []string{"escape/evil", "escape/a/b/c", "escape"} {
		w := s.do("POST", "/api/settings/storage/create", `{"path":"`+p+`"}`, s.admin)
		if w.Code != 400 {
			t.Errorf("create %s: %d %s", p, w.Code, w.Body)
		}
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("something was made outside the media folder: %v", ents)
	}
	// picking the link, or a folder under it
	for _, body := range []string{`{"folders":{"movies":"escape"}}`, `{"folders":{"movies":"escape/x"}}`,
		`{"folders":{"music":"` + filepath.Join(s.root, "escape") + `"}}`} {
		for _, c := range [][2]string{{"PUT", "/api/settings/storage"}, {"POST", "/api/settings/storage/check"}} {
			if w := s.do(c[0], c[1], body, s.admin); w.Code != 400 {
				t.Errorf("%s %s: %d %s", c[0], body, w.Code, w.Body)
			}
		}
	}
	if _, custom, _ := LoadFolderSettings(context.Background(), s.db); custom {
		t.Fatal("a link out of the media folder was saved")
	}
	select {
	case <-s.restarts:
		t.Fatal("restarted for a refused request")
	case <-time.After(50 * time.Millisecond):
	}
	// the link that stays inside works, for picking and for making a folder
	if w := s.do("POST", "/api/settings/storage/check", `{"folders":{"series":"alias"}}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := s.do("POST", "/api/settings/storage/create", `{"path":"alias/new"}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if st, err := os.Stat(filepath.Join(s.root, "tv", "new")); err != nil || !st.IsDir() {
		t.Fatal("the folder was not made behind the link")
	}
	// the picker lists the inside link and not the outside one
	v := s.view(s.do("GET", "/api/settings/storage", "", s.admin).Body)
	var names []string
	for _, e := range v.Entries {
		names = append(names, e.Name)
	}
	if strings.Contains(strings.Join(names, " "), "escape") || !strings.Contains(strings.Join(names, " "), "alias") {
		t.Fatalf("%v", names)
	}
}

func TestStorageMediaFolderThatIsItselfALink(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "media")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s := newStorageEnvWith(t, storageOpts{root: link, bare: true})
	if w := s.do("POST", "/api/settings/storage/create", `{"path":"movies/new"}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if st, err := os.Stat(filepath.Join(real, "movies", "new")); err != nil || !st.IsDir() {
		t.Fatal("not made in the folder the media link points to")
	}
}

func TestStorageRefusesFoldersInsideEachOther(t *testing.T) {
	s := newStorageEnv(t)
	for name, body := range map[string]string{
		"the books folder over the audiobooks": `{"folders":{"ebooks":"books"}}`, // audiobooks stays books/audiobooks
		"films with the series inside":         `{"folders":{"movies":"video","series":"video/series"}}`,
		"music over the book folders":          `{"folders":{"music":"books"}}`,
		"audiobooks over ebooks":               `{"folders":{"audiobooks":"books/ebooks/audio"}}`,
	} {
		w := s.do("PUT", "/api/settings/storage", body, s.admin)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "внутри папки другой") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if _, custom, _ := LoadFolderSettings(context.Background(), s.db); custom {
		t.Fatal("overlapping folders were saved")
	}
	// folders side by side are fine, books together or not
	w := s.do("PUT", "/api/settings/storage", `{"folders":{"ebooks":"Книги/эл","audiobooks":"Книги/аудио","comics":"Книги/манга"}}`, s.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	<-s.restarts
}

func TestStorageResetToAnOverlappingEnvironmentIsTheOwnersChoice(t *testing.T) {
	env := config.Folders{Movies: "video", Series: "video/series", Music: "music", Ebooks: "books/ebooks",
		Audiobooks: "books/audiobooks", Comics: "books/manga"}
	s := newStorageEnvWith(t, storageOpts{env: &env})
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('folders', ?)`,
		`{"movies":"movies","series":"tv","music":"music","ebooks":"books/ebooks","audiobooks":"books/audiobooks","comics":"books/manga"}`); err != nil {
		t.Fatal(err)
	}
	if w := s.do("PUT", "/api/settings/storage", `{"reset":true}`, s.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	<-s.restarts
	// but a folder picked now that lies inside another is refused
	if w := s.do("PUT", "/api/settings/storage", `{"folders":{"music":"video/m"}}`, s.admin); w.Code != 400 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestHealthAndSaveCarryTheBootID(t *testing.T) {
	s := newStorageEnv(t)
	var h struct {
		OK     bool   `json:"ok"`
		BootID string `json:"bootId"`
	}
	w := s.do("GET", "/api/health", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil || w.Code != 200 || !h.OK || h.BootID == "" || h.BootID != bootID {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// the answer that asks for the restart names the run that is about to stop
	w = s.do("PUT", "/api/settings/storage", `{"folders":{"series":"tv"}}`, s.admin)
	var out storagePut
	json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Restarting || out.BootID != h.BootID {
		t.Fatalf("%+v", out)
	}
	<-s.restarts
	if newBootID() == newBootID() {
		t.Fatal("two runs, one boot id")
	}
}
