// Command manifest writes a release's antresol-addon.json: the source
// manifest with the platforms and the web part filled in from the archives
// in -dir (<id>-<os>-<arch>.tar.gz, <id>-web.tar.gz) and their sha256. It
// first checks each archive the way the core will unpack it: regular files
// and directories only (no links, no pax global header, no AppleDouble
// "._" files), and a platform archive holds bin/addon (and each file named
// by -require: an addon that ships a pinned tool beside its program).
//
//	go run github.com/antresol-app/antresol/addonkit/cmd/manifest \
//	    -src antresol-addon.json -dir dist [-tag v1.2.3] [-require bin/tool,...]
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type asset struct {
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

func main() {
	src := flag.String("src", "antresol-addon.json", "the source manifest")
	dir := flag.String("dir", "dist", "the release directory (archives in, manifest out)")
	tag := flag.String("tag", "", "the release tag; must be v<version> when set")
	require := flag.String("require", "", "files besides bin/addon every platform archive must hold, comma-separated")
	flag.Parse()
	need := []string{"bin/addon"}
	if *require != "" {
		need = append(need, strings.Split(*require, ",")...)
	}
	if err := run(*src, *dir, *tag, need); err != nil {
		log.Fatalf("manifest: %v", err)
	}
}

// run writes dir/antresol-addon.json; every platform archive must hold the
// files need names.
func run(src, dir, tag string, need []string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	id, _ := m["id"].(string)
	version, _ := m["version"].(string)
	if id == "" || version == "" {
		return fmt.Errorf("%s: id and version are required", src)
	}
	if tag != "" && tag != "v"+version {
		return fmt.Errorf("tag %s does not match version %s (want v%s)", tag, version, version)
	}
	platRe := regexp.MustCompile("^" + regexp.QuoteMeta(id) + `-([a-z0-9]+)-([a-z0-9]+)\.tar\.gz$`)
	names, err := filepath.Glob(filepath.Join(dir, "*.tar.gz"))
	if err != nil {
		return err
	}
	sort.Strings(names)
	platforms := map[string]asset{}
	delete(m, "web")
	for _, p := range names {
		name := filepath.Base(p)
		sum, files, err := inspect(p)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if name == id+"-web.tar.gz" {
			if !files["index.js"] {
				return fmt.Errorf("%s: no index.js at its root", name)
			}
			m["web"] = asset{name, sum}
			continue
		}
		g := platRe.FindStringSubmatch(name)
		if g == nil {
			return fmt.Errorf("%s: not <id>-<os>-<arch>.tar.gz or <id>-web.tar.gz", name)
		}
		for _, f := range need {
			if !files[f] {
				return fmt.Errorf("%s: no %s", name, f)
			}
		}
		platforms[g[1]+"/"+g[2]] = asset{name, sum}
	}
	if len(platforms) == 0 {
		return fmt.Errorf("%s: no platform archives", dir)
	}
	m["platforms"] = platforms
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "antresol-addon.json"), append(out, '\n'), 0o644)
}

// inspect returns the archive's sha256 and its regular files, refusing what
// the core's unpacking refuses.
func inspect(p string) (string, map[string]bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	h := sha256.New()
	tee := io.TeeReader(f, h)
	gz, err := gzip.NewReader(tee)
	if err != nil {
		return "", nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string]bool{}
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		name := path.Clean(hd.Name)
		if strings.HasPrefix(path.Base(name), "._") {
			return "", nil, fmt.Errorf("%s: an AppleDouble file (make the archive with COPYFILE_DISABLE=1)", hd.Name)
		}
		switch hd.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			files[name] = true
		case tar.TypeXGlobalHeader:
			return "", nil, fmt.Errorf("a pax global header (git archive makes one; use plain tar)")
		default:
			return "", nil, fmt.Errorf("%s: not a regular file or directory", hd.Name)
		}
	}
	// the rest of the stream counts towards the checksum too
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), files, nil
}
