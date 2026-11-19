package mediacheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mountinfo = `22 1 0:21 / / rw,relatime - overlay overlay rw,lowerdir=/x
23 22 0:22 / /proc rw,nosuid - proc proc rw
24 22 0:23 / /dev rw,nosuid - tmpfs tmpfs rw
30 22 8:1 /srv/media /media rw,relatime - ext4 /dev/sda1 rw
31 22 8:1 /srv/config /config rw,relatime - ext4 /dev/sda1 rw
32 22 8:17 /adult /mnt/adult\040films rw,relatime - ext4 /dev/sdb1 rw
33 22 8:1 /etc/hosts /etc/hosts rw,relatime - ext4 /dev/sda1 rw
34 22 0:40 / /sys/fs/cgroup ro - cgroup2 cgroup rw
35 30 8:33 / /media/usb rw - vfat /dev/sdc1 rw
36 22 0:50 / /run/secrets ro - tmpfs tmpfs rw
`

func TestMountPointsKeepsWhatMayHoldMedia(t *testing.T) {
	got := MountPoints(strings.NewReader(mountinfo))
	want := []string{"/media", "/config", "/mnt/adult films", "/media/usb"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}

func TestPlacesLeaveOutTheServersOwnAndWhatIsInside(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	other := filepath.Join(root, "adult")
	cfg := filepath.Join(root, "config")
	file := filepath.Join(root, "a-file")
	for _, d := range []string{media, other, cfg, filepath.Join(media, "usb")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(file, []byte("x"), 0o644)
	got := Places(media, []string{other, cfg, filepath.Join(media, "usb"), file, "/", "/etc/hosts"}, []string{filepath.Join(cfg, "db")})
	if strings.Join(got, "|") != media+"|"+other {
		t.Fatalf("%q", got)
	}
	// a mount that holds the server's settings is not a place either
	got = Places(media, []string{root}, []string{cfg})
	if strings.Join(got, "|") != media {
		t.Fatalf("%q", got)
	}
}

// the browser never leaves the places: not by "..", not by a path outside,
// not by a link (to a folder or through a parent) that leads out
func TestConfine(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	other := filepath.Join(root, "other")
	secret := filepath.Join(root, "secret")
	for _, d := range []string{filepath.Join(media, "movies"), other, secret} {
		os.MkdirAll(d, 0o755)
	}
	os.Symlink(secret, filepath.Join(media, "out"))
	os.Symlink(filepath.Join(other), filepath.Join(media, "to-other"))
	os.Symlink("/", filepath.Join(media, "slash"))
	places := []string{media, other}
	for _, c := range []struct {
		path string
		ok   bool
	}{
		{media, true},
		{filepath.Join(media, "movies"), true},
		{filepath.Join(media, "movies", "not-made-yet"), true},
		{other, true},
		{filepath.Join(media, "to-other"), true}, // a link into another place
		{filepath.Join(media, "out"), false},
		{filepath.Join(media, "out", "x"), false},
		{filepath.Join(media, "slash", "etc"), false},
		{media + "/../secret", false},
		{media + "/movies/../../secret", false},
		{secret, false},
		{"relative/movies", false},
		{"", false},
		{media + "\x00", false},
		{"/", false},
		{"/etc", false},
		{media + "-twin", false}, // a name that only starts the same
	} {
		_, err := Confine(c.path, places)
		if (err == nil) != c.ok {
			t.Errorf("Confine(%q) = %v, want ok=%v", c.path, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrOutside) {
			t.Errorf("%q: %v", c.path, err)
		}
	}
}

func TestBrowseListsFoldersAndDropsLinksOut(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	secret := filepath.Join(root, "secret")
	for _, d := range []string{filepath.Join(media, "Фильмы", "A"), filepath.Join(media, "adult"), filepath.Join(media, ".hidden"), secret} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(media, "Фильмы", "a.mkv"), nil, 0o644)
	os.WriteFile(filepath.Join(media, "file.txt"), nil, 0o644)
	os.Symlink(secret, filepath.Join(media, "out"))
	os.Symlink(filepath.Join(media, "adult"), filepath.Join(media, "in"))
	subs, truncated, err := Browse(media, []string{media})
	if err != nil || truncated {
		t.Fatal(err)
	}
	var names []string
	for _, s := range subs {
		names = append(names, s.Name)
	}
	if strings.Join(names, "|") != "adult|in|Фильмы" {
		t.Fatalf("%q", names)
	}
	if subs[2].Items != 2 || subs[2].Path != filepath.Join(media, "Фильмы") {
		t.Fatalf("%+v", subs[2])
	}
}
