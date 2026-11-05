package files

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMountinfo(t *testing.T) {
	text := `22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw
30 22 8:1 /srv/media /media rw,relatime - ext4 /dev/sda1 rw
31 22 8:1 /srv/data /data rw,relatime - ext4 /dev/sda1 rw
32 30 8:1 /srv/x /media/My\040Films rw - ext4 /dev/sda1 rw
`
	points := parseMountinfo(text)
	if strings.Join(points, "|") != "/|/media|/data|/media/My Films" {
		t.Fatalf("%q", points)
	}
	for p, want := range map[string]string{
		"/media/movies/a.mkv": "/media", "/data/.vault-trash": "/data", "/media/My Films/x": "/media/My Films",
		"/etc/hosts": "/", "/mediafiles/x": "/",
	} {
		if got, ok := deepestMount(points, p); !ok || got != want {
			t.Errorf("%s: %q %v", p, got, ok)
		}
	}
}

// twoMounts makes mr/media a mount of its own beside mr (MediaRoot), like
// /media and /data bind-mounted from one disk.
func twoMounts(s *Sandbox, mr string) {
	media := filepath.Join(mr, "media")
	s.MountOf = func(p string) (string, bool) {
		real, _ := filepath.EvalSymlinks(media)
		if within(real, p) || within(media, p) {
			return real, true
		}
		r, _ := filepath.EvalSymlinks(mr)
		return r, true
	}
}

// A path on another mount than MediaRoot goes to that mount's own trash;
// the list, restore and delete find it there; the trash stays hidden and
// out of reach of the file browser.
func TestTrashPerMount(t *testing.T) {
	o, mr, _ := testOps(t)
	twoMounts(o.Sandbox, mr)
	ctx := context.Background()
	write(t, filepath.Join(mr, "media/movies/Брат (1997)/Брат.mkv"), 5)
	write(t, filepath.Join(mr, "torrents/x.mkv"), 3)

	res, err := o.Trash(ctx, "movies", []string{"Брат (1997)"}, false, 1)
	if err != nil || len(res.Trashed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	realMedia, _ := filepath.EvalSymlinks(filepath.Join(mr, "media"))
	mediaTrash := filepath.Join(realMedia, TrashDirName)
	if !strings.HasPrefix(res.Trashed[0].at, mediaTrash+string(filepath.Separator)) {
		t.Fatalf("trashed into %s, not %s", res.Trashed[0].at, mediaTrash)
	}
	if _, err := o.Trash(ctx, "downloads", []string{"x.mkv"}, true, 1); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(mr, TrashDirName)) == false {
		t.Fatal("MediaRoot's own files left its trash")
	}
	items, err := o.ListTrash(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("list %+v %v", items, err)
	}
	if got := o.Sandbox.TrashDirs(); len(got) != 2 {
		t.Fatalf("trash dirs %q", got)
	}

	// hidden and out of reach
	if _, err := o.Sandbox.Resolve("library", TrashDirName+"/x"); !errors.Is(err, ErrOutside) {
		t.Fatalf("resolve into the mount's trash: %v", err)
	}
	b := &Browser{Sandbox: o.Sandbox, ThumbURL: ThumbURL}
	l, err := b.List(ctx, "library", "", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Name == TrashDirName {
			t.Fatal("trash listed")
		}
	}

	// restore back to the original place; delete the other for good
	var brat, x TrashItem
	for _, it := range items {
		if it.Name == "Брат (1997)" {
			brat = it
		} else {
			x = it
		}
	}
	if err := o.Restore(ctx, brat.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(mr, "media/movies/Брат (1997)/Брат.mkv")) {
		t.Fatal("not restored")
	}
	if err := o.Delete(ctx, x.ID); err != nil {
		t.Fatal(err)
	}

	// a row naming a trash the sandbox does not know is refused
	write(t, filepath.Join(mr, "media/movies/y.mkv"), 1)
	res, err = o.Trash(ctx, "movies", []string{"y.mkv"}, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.DB.Exec(`UPDATE trash SET trash_dir='/etc' WHERE id=?`, res.Trashed[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := o.Delete(ctx, res.Trashed[0].ID); !errors.Is(err, ErrOutside) {
		t.Fatalf("unknown trash: %v", err)
	}
}

// The library delete moves into the movie's own mount's trash; a trash
// on another mount than the file refuses before anything moves.
func TestItemDeletePerMountTrash(t *testing.T) {
	e := newItemEnv(t)
	twoMounts(e.d.Ops.Sandbox, e.mr)
	film := filepath.Join(e.mv, "Брат (1997)", "Брат (1997).mkv")
	write(t, film, 4)
	e.rescan(t)
	it := e.itemOf(t, film)
	ctx := context.Background()

	// a resolver that puts the mount point itself elsewhere: the trash
	// (made at the mount point) would not be on the file's mount
	good := e.d.Ops.Sandbox.MountOf
	media := filepath.Join(e.mr, "media")
	e.d.Ops.Sandbox.MountOf = func(p string) (string, bool) {
		if real, _ := filepath.EvalSymlinks(media); p == real || p == media {
			return "/elsewhere", true
		}
		return good(p)
	}
	if _, err := e.d.Delete(ctx, it.ID, 1); !errors.Is(err, ErrCrossDevice) || !exists(film) {
		t.Fatalf("mismatched mount: %v", err)
	}
	e.d.Ops.Sandbox.MountOf = good

	res, err := e.d.Delete(ctx, it.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	realMedia, _ := filepath.EvalSymlinks(media)
	if !strings.HasPrefix(res.Trashed[0].at, filepath.Join(realMedia, TrashDirName)) {
		t.Fatalf("into %s", res.Trashed[0].at)
	}
	if err := e.d.Ops.Restore(ctx, res.Trashed[0].ID); err != nil || !exists(film) {
		t.Fatalf("restore: %v", err)
	}
}
