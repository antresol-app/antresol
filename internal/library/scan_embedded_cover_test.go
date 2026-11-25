package library

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// artFiles lists what the scan saved in the scanner's ArtDir.
func artFiles(t *testing.T, s *Scanner) []string {
	t.Helper()
	es, err := os.ReadDir(s.ArtDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// An album folder with a cover file needs no embedded picture: none is
// saved, and the cover is the file.
func TestAlbumWithACoverFileSavesNoEmbeddedPicture(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1,
		testJPEG(t, color.RGBA{255, 0, 0, 255}))
	writeAudio(t, filepath.Join(dir, "02.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "2"}, 1,
		testJPEG(t, color.RGBA{0, 255, 0, 255}))
	cover := filepath.Join(dir, "cover.jpg")
	touch(t, cover, string(testJPEG(t, color.RGBA{0, 0, 255, 255})))
	scanMusicOK(t, s)
	if al, _ := oneAlbum(t, s.Repo); al.Poster != "file:"+cover {
		t.Fatalf("poster %q", al.Poster)
	}
	if got := artFiles(t, s); len(got) != 0 {
		t.Fatalf("embedded pictures saved beside a cover file: %v", got)
	}
}

// A book takes the picture of its first file that has one; the pictures of
// the others are never saved.
func TestAudiobookSavesOnlyTheFirstEmbeddedPicture(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Без имени")
	first := testJPEG(t, color.RGBA{200, 30, 30, 255})
	writeAudio(t, filepath.Join(dir, "a1.mp3"), map[string]string{"album": "Мастер и Маргарита", "title": "Часть первая"}, 1, nil)
	writeAudio(t, filepath.Join(dir, "a2.mp3"), map[string]string{"album": "Мастер и Маргарита", "title": "Часть вторая"}, 1, first)
	writeAudio(t, filepath.Join(dir, "a3.mp3"), map[string]string{"album": "Мастер и Маргарита", "title": "Часть третья"}, 1,
		testJPEG(t, color.RGBA{30, 200, 30, 255}))
	scanAudiobooksOK(t, s)
	b, _ := oneAudiobook(t, s.Repo)
	if b.Poster != coverName(first, "jpg") {
		t.Fatalf("poster %q", b.Poster)
	}
	if got := artFiles(t, s); len(got) != 1 || got[0] != b.Poster {
		t.Fatalf("saved %v, want only %s", got, b.Poster)
	}
}
