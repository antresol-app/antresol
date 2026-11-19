package channels

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/imgcache"
)

// A generated channel of a franchise or a series gets a logo by itself:
// the title logo the TMDB enricher already keeps for the library's titles
// (meta logo, in <data>/images): the series' own, and for a franchise the
// logo of its title whose name is the franchise's («Звёздные войны»), else
// of its first title that has one. A raster logo only (an SVG one is
// passed over). An admin's upload wins, and a logo the admin removed is
// not looked for again (channels.logo_auto).
const (
	LogoAutoNone    = 0 // no logo, or an uploaded one
	LogoAutoFound   = 1 // the logo was found automatically
	LogoAutoRemoved = 2 // an admin removed the logo: not looked for again
)

// autoLogos gives the generated channels of franchises and series that
// have no logo the logo of their titles. A channel no title has a logo for
// is tried again on the next refresh (the enricher may fetch one).
func (s *Service) autoLogos(ctx context.Context) {
	if s.DataDir == "" {
		return
	}
	all, err := s.Store.All(ctx)
	if err != nil {
		return
	}
	var fs map[string]Franchise
	for _, c := range all {
		if c.External() || c.LogoVersion != 0 || c.LogoAuto != LogoAutoNone {
			continue
		}
		var ids []int64
		name := c.Name
		switch keyKind(c.AutoKey) {
		case "series":
			id, err := strconv.ParseInt(strings.TrimPrefix(c.AutoKey, "series:"), 10, 64)
			if err != nil {
				continue
			}
			ids = []int64{id}
		case "franchise":
			if fs == nil {
				fs = map[string]Franchise{}
				list, err := s.Store.Franchises(ctx)
				if err != nil {
					return
				}
				for _, f := range list {
					fs[f.Key] = f
				}
			}
			f, ok := fs[strings.TrimPrefix(c.AutoKey, "franchise:")]
			if !ok {
				continue
			}
			name = f.Name
			ids = f.Effective()
		default:
			continue
		}
		png := s.titleLogo(ctx, name, ids)
		if png == nil {
			continue
		}
		if _, err := s.saveLogo(ctx, c, png, LogoAutoFound); err != nil && !errors.Is(err, ErrNotFound) {
			log.Printf("channels: logo of %s: %v", c.AutoKey, err)
		}
	}
}

// titleLogo is the logo of the title of ids named like name (case and
// punctuation aside), else of the first one that has a usable logo, fitted
// (FitLogo); nil: none.
func (s *Service) titleLogo(ctx context.Context, name string, ids []int64) []byte {
	type cand struct {
		title, ref string
	}
	var cands []cand
	for _, id := range ids {
		var title string
		var ref sql.NullString
		err := s.Store.DB.QueryRowContext(ctx, `SELECT title, json_extract(meta_json,'$.logo') FROM items WHERE id=?`, id).Scan(&title, &ref)
		if err != nil || !ref.Valid || ref.String == "" {
			continue
		}
		cands = append(cands, cand{title, ref.String})
	}
	want := logoKey(name)
	// the title named as the franchise first, the rest in the franchise's order
	for pass := 0; pass < 2; pass++ {
		for _, c := range cands {
			if pass == 0 && logoKey(c.title) != want {
				continue
			}
			p, err := imgcache.Resolve(filepath.Join(s.DataDir, "images"), c.ref)
			if err != nil {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil || len(b) > 4*MaxLogoBytes {
				continue
			}
			if png, err := FitLogo(b); err == nil {
				return png
			}
		}
	}
	return nil
}

// logoKey is a name compared case- and punctuation-blind («Звёздные
// войны» = «звездные войны»).
func logoKey(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == 0x451 { // ё → е
			return 0x435
		}
		return r
	}, strings.ToLower(s))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' || r >= '0' && r <= '9')
	}), " ")
}
