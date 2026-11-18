package mediacheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noStray(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TestPrefix) {
			t.Fatalf("the write test left %s behind", e.Name())
		}
	}
}

func TestCheckFound(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "movies")
	touch(t, filepath.Join(dir, "A (2001)", "A (2001).mkv"))
	touch(t, filepath.Join(dir, "B (2002)", "B (2002).mkv"))
	touch(t, filepath.Join(dir, "loose.mkv"))
	touch(t, filepath.Join(dir, ".hidden"))
	d := Check(dir)
	if d.Status != StatusOK || d.Items != 3 || d.More || d.Error != "" {
		t.Fatalf("%+v", d)
	}
	noStray(t, dir)
}

func TestCheckMissingAndNotDir(t *testing.T) {
	root := t.TempDir()
	if d := Check(filepath.Join(root, "nope")); d.Status != StatusMissing || d.Items != 0 {
		t.Fatalf("%+v", d)
	}
	touch(t, filepath.Join(root, "file"))
	if d := Check(filepath.Join(root, "file")); d.Status != StatusNotDir {
		t.Fatalf("%+v", d)
	}
	// nothing was created by looking
	if _, err := os.Stat(filepath.Join(root, "nope")); err == nil {
		t.Fatal("Check created the folder")
	}
}

func TestCheckCountsAreCapped(t *testing.T) {
	old := CountCap
	CountCap = 5
	defer func() { CountCap = old }()
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		touch(t, filepath.Join(root, fmt.Sprintf("f%d", i)))
	}
	if d := Check(root); d.Items != 5 || !d.More {
		t.Fatalf("%+v", d)
	}
}

func TestWriteTestLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := WriteTest(dir); err != nil {
		t.Fatal(err)
	}
	noStray(t, dir)
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("%v", ents)
	}
}

func TestNotWritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := filepath.Join(t.TempDir(), "series")
	touch(t, filepath.Join(dir, "S", "S.mkv"))
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	d := Check(dir)
	if d.Status != StatusReadOnly || d.Why != WhyDenied || d.Error == "" || d.Items != 1 {
		t.Fatalf("%+v", d)
	}
	// a folder nobody may list
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	if d := Check(dir); d.Status != StatusUnreadable || d.Why != WhyDenied {
		t.Fatalf("%+v", d)
	}
}

func TestWriteFailureKinds(t *testing.T) {
	old := createTemp
	defer func() { createTemp = old }()
	for _, c := range []struct {
		err  error
		why  string
		want Status
	}{
		{&os.PathError{Op: "open", Path: "/media/x", Err: syscall.EACCES}, WhyDenied, StatusReadOnly},
		{&os.PathError{Op: "open", Path: "/media/x", Err: syscall.EPERM}, WhyDenied, StatusReadOnly},
		{&os.PathError{Op: "open", Path: "/media/x", Err: syscall.EROFS}, WhyReadOnlyFS, StatusReadOnly},
		{&os.PathError{Op: "open", Path: "/media/x", Err: syscall.ENOSPC}, WhyOther, StatusReadOnly},
	} {
		createTemp = func(string, string) (*os.File, error) { return nil, c.err }
		d := Check(t.TempDir())
		if d.Status != c.want || d.Why != c.why {
			t.Errorf("%v: %+v", c.err, d)
		}
		if !strings.Contains(d.Error, c.err.Error()) {
			t.Errorf("error text %q lacks %q", d.Error, c.err)
		}
	}
	if Classify(errors.New("boom")) != WhyOther || Classify(nil) != "" {
		t.Fatal("classify")
	}
}

func TestCheckTranscodeCreatesItsFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tmp", "antresol-hls")
	d := CheckTranscode(dir, "")
	if d.Status != StatusOK {
		t.Fatalf("%+v", d)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatal("the transcode folder was not made")
	}
	noStray(t, dir)
}

func TestCheckTranscodeCannotCreate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	parent := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o755)
	d := CheckTranscode(filepath.Join(parent, "tmp", "antresol-hls"), "")
	if d.Status != StatusReadOnly || d.Why != WhyDenied {
		t.Fatalf("%+v", d)
	}
}

func TestCheckTranscodeNeverMakesTheMediaDisk(t *testing.T) {
	// the media folder is not there (an unmounted disk, a missing volume):
	// a transcode folder inside it is not made, which would also make the
	// media folder itself on the disk under it
	base := t.TempDir()
	root := filepath.Join(base, "media")
	dir := filepath.Join(root, "tmp", "antresol-hls")
	if d := CheckTranscode(dir, root); d.Status != StatusMissing {
		t.Fatalf("%+v", d)
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("made the media folder")
	}
	// a file in the media folder's place is no folder either
	touch(t, root)
	if d := CheckTranscode(dir, root); d.Status != StatusMissing {
		t.Fatalf("%+v", d)
	}
	if st, err := os.Stat(root); err != nil || st.IsDir() {
		t.Fatal("the file in the media folder's place was touched")
	}
	// with the media folder there it is made
	os.Remove(root)
	mkdirs(t, base, "media")
	if d := CheckTranscode(dir, root); d.Status != StatusOK {
		t.Fatalf("%+v", d)
	}
}

func TestNearestWithin(t *testing.T) {
	base := t.TempDir()
	mkdirs(t, base, "media/a")
	media := filepath.Join(base, "media")
	// a missing chain: the nearest folder that exists, inside the limit
	if got, ok := NearestWithin(filepath.Join(media, "a", "b", "c"), media); !ok || got != filepath.Join(media, "a") {
		t.Fatalf("%q %v", got, ok)
	}
	// the limit itself is there and is the nearest
	if got, ok := NearestWithin(filepath.Join(media, "x", "y"), media); !ok || got != media {
		t.Fatalf("%q %v", got, ok)
	}
	// the limit is not there: nothing above it is offered
	gone := filepath.Join(base, "gone")
	if got, ok := NearestWithin(filepath.Join(gone, "tmp", "hls"), gone); ok || got != "" {
		t.Fatalf("%q %v", got, ok)
	}
	// a path outside the limit is not the limit's to fix
	if got, ok := NearestWithin(filepath.Join(base, "other"), media); ok {
		t.Fatalf("%q", got)
	}
	// a folder that is there is its own nearest
	if got, ok := NearestWithin(media, media); !ok || got != media {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestInside(t *testing.T) {
	for _, c := range []struct {
		base, p string
		want    bool
	}{
		{"/media", "/media", true},
		{"/media/", "/media/tv", true},
		{"/media", "/media/../etc", false},
		{"/media", "/media2", false},
		{"/media", "/", false},
	} {
		if got := Inside(c.base, c.p); got != c.want {
			t.Errorf("Inside(%q, %q) = %v", c.base, c.p, got)
		}
	}
}

func TestResolveFollowsLinksOfAFolderNotMadeYet(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdirs(t, base, "media", "elsewhere")
	if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(base, "media", "link")); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(filepath.Join(base, "media", "link", "new", "deeper"))
	if err != nil || got != filepath.Join(base, "elsewhere", "new", "deeper") {
		t.Fatalf("%q %v", got, err)
	}
	// a link that leads nowhere cannot be judged
	if err := os.Symlink(filepath.Join(base, "gone"), filepath.Join(base, "media", "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(filepath.Join(base, "media", "dangling", "x")); err == nil {
		t.Fatal("a dangling link was resolved")
	}
}

func TestUnmounted(t *testing.T) {
	base := t.TempDir()
	if !Unmounted(filepath.Join(base, "nope")) {
		t.Fatal("a missing folder is not mounted")
	}
	mkdirs(t, base, "empty", "disk/tv")
	touch(t, filepath.Join(base, "empty", ".hidden"))
	if !Unmounted(filepath.Join(base, "empty")) {
		t.Fatal("an empty mount point is not a disk")
	}
	if Unmounted(filepath.Join(base, "disk")) {
		t.Fatal("a folder with content is a disk")
	}
	touch(t, filepath.Join(base, "file"))
	if !Unmounted(filepath.Join(base, "file")) {
		t.Fatal("a file is no disk")
	}
}

func TestSubfolders(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "Фильмы", "tv", ".trash", "books/ebooks", "books/manga", "empty")
	touch(t, filepath.Join(root, "Фильмы", "a.mkv"))
	touch(t, filepath.Join(root, "Фильмы", "b.mkv"))
	touch(t, filepath.Join(root, "tv", "S", "e.mkv"))
	touch(t, filepath.Join(root, "books", "ebooks", "x.epub"))
	touch(t, filepath.Join(root, "readme.txt"))
	got, err := Subfolders(root, []string{"books"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	items := map[string]int{}
	for _, e := range got {
		names = append(names, e.Name)
		items[e.Name] = e.Items
	}
	want := "books books/ebooks books/manga empty tv Фильмы"
	if strings.Join(names, " ") != want {
		t.Fatalf("%v", names)
	}
	if items["Фильмы"] != 2 || items["tv"] != 1 || items["books/ebooks"] != 1 || items["empty"] != 0 || items["books"] != 2 {
		t.Fatalf("%v", items)
	}
	if _, err := Subfolders(filepath.Join(root, "nope"), nil); err == nil {
		t.Fatal("a missing root listed")
	}
}

func TestSubfoldersLeaveOutLinksThatLeaveTheMediaFolder(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	mkdirs(t, base, "media/real", "outside")
	touch(t, filepath.Join(base, "outside", "x.mkv"))
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := Subfolders(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	// the link inside the media folder is a folder; the one that leaves it is not offered
	if strings.Join(names, " ") != "alias real" {
		t.Fatalf("%v", names)
	}
}

func TestSuggestPrefersTheFolderWithFiles(t *testing.T) {
	// a new install: the server made empty default folders, and the real
	// one sits beside them under its own name
	got := Suggest([]Entry{
		{Name: "Movies", Items: 0}, {Name: "movies", Items: 0}, {Name: "Фильмы", Items: 45},
		{Name: "series", Items: 0}, {Name: "TV", Items: 7}, {Name: "Сериалы", Items: 12},
		{Name: "music", Items: 0},
	})
	if got[Movies] != "Фильмы" {
		t.Errorf("movies: %q", got[Movies])
	}
	if got[Series] != "Сериалы" && got[Series] != "TV" {
		t.Errorf("series: %q", got[Series])
	}
	if got[Series] == "series" {
		t.Errorf("an empty folder beat one with files")
	}
	// nothing but the empty default: that is the answer (the caller sees it is taken)
	if got[Music] != "music" {
		t.Errorf("music: %q", got[Music])
	}
	// files beat depth: the shallow folder with files over a deeper empty one
	got = Suggest([]Entry{{Name: "ebooks", Items: 3}, {Name: "books/ebooks", Items: 0}})
	if got[Ebooks] != "ebooks" {
		t.Errorf("ebooks: %q", got[Ebooks])
	}
}

func TestSuggest(t *testing.T) {
	e := func(names ...string) []Entry {
		var out []Entry
		for _, n := range names {
			out = append(out, Entry{Name: n})
		}
		return out
	}
	got := Suggest(e("Фильмы", "Сериалы", "Музыка", "books", "books/ebooks", "books/audiobooks", "books/Manga", "tmp"))
	want := map[Kind]string{Movies: "Фильмы", Series: "Сериалы", Music: "Музыка",
		Ebooks: "books/ebooks", Audiobooks: "books/audiobooks", Comics: "books/Manga"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%v", got)
	}
	// nothing that looks like a kind: nothing is guessed
	if got := Suggest(e("stuff", "backup")); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// a plain English layout
	got = Suggest(e("Movies", "TV Shows", "Audiobooks"))
	if got[Movies] != "Movies" || got[Series] != "TV Shows" || got[Audiobooks] != "Audiobooks" {
		t.Fatalf("%v", got)
	}
}

func TestDockerFixForPermissionDenied(t *testing.T) {
	f := FixFor(FixInput{
		Bad:     []Bad{{Path: "/media/movies", Why: WhyDenied}, {Path: "/media/movies/old", Why: WhyDenied}},
		Service: "antresol", ConfigDir: "/config", Docker: true,
		Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 1001, 1002, true },
	})
	if f.Chown != "docker compose run --rm --no-deps --user root --entrypoint chown antresol -R 1000:1000 /media/movies" {
		t.Fatalf("%q", f.Chown)
	}
	if f.ComposeUser != `user: "1001:1002"` {
		t.Fatalf("%q", f.ComposeUser)
	}
	if f.ConfigChown != "docker compose run --rm --no-deps --user root --entrypoint chown antresol -R 1001:1002 /config" {
		t.Fatalf("%q", f.ConfigChown)
	}
	if f.UID != 1000 || f.GID != 1000 || f.Owner != "1001:1002" || !f.Docker {
		t.Fatalf("%+v", f)
	}
	if strings.Join(f.Paths, " ") != "/media/movies" {
		t.Fatalf("a path under another failing one was listed: %v", f.Paths)
	}
}

func TestFixOnTheHost(t *testing.T) {
	f := FixFor(FixInput{Bad: []Bad{{Path: "/srv/my media/music", Why: WhyDenied}}, Service: "antresol",
		Self: Identity{UID: 501, GID: 20}, Owner: func(string) (int, int, bool) { return 0, 0, true }})
	if f.Chown != "sudo chown -R 501:20 '/srv/my media/music'" {
		t.Fatalf("%q", f.Chown)
	}
	// the owner is root: running the server as root is no advice
	if f.ComposeUser != "" || f.ConfigChown != "" {
		t.Fatalf("%+v", f)
	}
}

func TestFixNeedsNoChownForReadOnlyMount(t *testing.T) {
	f := FixFor(FixInput{Bad: []Bad{{Path: "/media", Why: WhyReadOnlyFS}}, Service: "antresol", Docker: true,
		Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 1000, 1000, true }})
	if f.Chown != "" || f.ComposeUser != "" || len(f.Paths) != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestFixOwnerEqualsServer(t *testing.T) {
	// the files are the server's own: the mode bits are the trouble, not an owner
	f := FixFor(FixInput{Bad: []Bad{{Path: "/media/tv", Why: WhyDenied}}, Service: "antresol", Docker: true,
		Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 1000, 1000, true }})
	if f.ComposeUser != "" {
		t.Fatalf("%+v", f)
	}
	if f.Chown == "" {
		t.Fatal("still offers the chown")
	}
}

func TestFixNeverHandsOverTheRootOfTheFileSystem(t *testing.T) {
	for _, docker := range []bool{true, false} {
		f := FixFor(FixInput{Bad: []Bad{{Path: "/", Why: WhyDenied}, {Path: "//", Why: WhyDenied}}, Service: "antresol", Docker: docker,
			Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 0, 0, true }})
		if f.Chown != "" || len(f.Paths) != 0 {
			t.Fatalf("docker %v: %+v", docker, f)
		}
	}
}

func TestFixNamesAMissingMountInsteadOfAChown(t *testing.T) {
	f := FixFor(FixInput{Mount: []string{"/media/tmp/antresol-hls", "/media/tmp/antresol-hls/sub", "/"}, Docker: true,
		Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 0, 0, true }})
	if strings.Join(f.Mount, " ") != "/media/tmp/antresol-hls" || f.Chown != "" || len(f.Paths) != 0 {
		t.Fatalf("%+v", f)
	}
	// a mount to fix beside a folder to hand over: both are said
	f = FixFor(FixInput{Bad: []Bad{{Path: "/media/tv", Why: WhyDenied}}, Mount: []string{"/scratch/hls"}, Docker: true,
		Self: Identity{UID: 1000, GID: 1000}, Owner: func(string) (int, int, bool) { return 1000, 1000, true }})
	if len(f.Mount) != 1 || !strings.HasSuffix(f.Chown, " /media/tv") {
		t.Fatalf("%+v", f)
	}
}

func TestNearestExisting(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a")
	if got := NearestExisting(filepath.Join(root, "a", "b", "c")); got != filepath.Join(root, "a") {
		t.Fatalf("%q", got)
	}
	if got := NearestExisting(filepath.Join(root, "a")); got != filepath.Join(root, "a") {
		t.Fatalf("%q", got)
	}
}

func TestSelfIdentity(t *testing.T) {
	id := Self()
	if id.UID != os.Getuid() || id.GID != os.Getgid() {
		t.Fatalf("%+v", id)
	}
	if uid, gid, ok := OwnerOf(t.TempDir()); !ok || uid != os.Getuid() || gid < 0 {
		t.Fatalf("%d %d %v", uid, gid, ok)
	}
}
