package segments

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestLiveSeason runs the season analysis on fingerprints dumped from a
// real server (skipped without SEGMENTS_LIVE_DIR). The folder holds
// <id>.head / <id>.tail (raw chromaprint), <id>.black (blackdetect's log
// of the tail window, keyframes only) and index lines "<id> <durationMs>
// <tailFromSec> <season key> <episode>"; it prints what each season gets.
func TestLiveSeason(t *testing.T) {
	dir := os.Getenv("SEGMENTS_LIVE_DIR")
	if dir == "" {
		t.Skip("SEGMENTS_LIVE_DIR not set")
	}
	f, err := os.Open(filepath.Join(dir, "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	read := func(p string) []uint32 {
		b, _ := os.ReadFile(p)
		out := make([]uint32, len(b)/4)
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(b[i*4:])
		}
		return out
	}
	blackRe := regexp.MustCompile(`black_start:\s*([0-9.]+)\s+black_end:\s*([0-9.]+)`)
	seasons := map[string][]Episode{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 5 {
			continue
		}
		id, _ := strconv.ParseInt(fl[0], 10, 64)
		dur, _ := strconv.ParseFloat(fl[1], 64)
		from, _ := strconv.ParseFloat(fl[2], 64)
		n, _ := strconv.Atoi(fl[4])
		e := Episode{File: id, Item: id, Number: n, DurSec: dur / 1000, TailAt: from,
			Head: read(filepath.Join(dir, fl[0]+".head")), Tail: read(filepath.Join(dir, fl[0]+".tail"))}
		bl, _ := os.ReadFile(filepath.Join(dir, fl[0]+".black"))
		for _, m := range blackRe.FindAllStringSubmatch(string(bl), -1) {
			s, _ := strconv.ParseFloat(m[1], 64)
			en, _ := strconv.ParseFloat(m[2], 64)
			e.Black = append(e.Black, Span{from + s, from + en})
		}
		seasons[fl[3]] = append(seasons[fl[3]], e)
	}
	for name, eps := range seasons {
		intros, creds := FindIntros(eps), FindCredits(eps)
		stings := FindStings(eps, intros)
		sortEpisodes(eps)
		fmt.Printf("== %s: %d episodes, intro %d, credits %d, stings %d\n", name, len(eps), len(intros), len(creds), len(stings))
		ids := make([]int, 0, len(eps))
		for _, e := range eps {
			ids = append(ids, int(e.File))
		}
		sort.Ints(ids)
		for _, e := range eps {
			in, iok := intros[e.File]
			cr, cok := creds[e.File]
			line := fmt.Sprintf("E%02d dur %7.1f", e.Number, e.DurSec)
			if iok {
				line += fmt.Sprintf("  intro %6.1f–%6.1f (%4.1f s)", in.Start, in.End, in.Len())
			} else {
				line += "  intro          —           "
			}
			if cok {
				line += fmt.Sprintf("  credits %7.1f–%7.1f (−%5.1f s)", cr.Start, cr.End, e.DurSec-cr.Start)
			} else {
				line += "  credits —"
			}
			if st, ok := stings[e.File]; ok {
				line += fmt.Sprintf("  sting→%.1f clip=%v", st, EarlierClip(eps, e.File, st, in.Start))
			}
			fmt.Println(line)
		}
	}
}
