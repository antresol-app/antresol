// Package config loads Vault configuration from VAULT_* environment variables.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/mediaimport"
)

type Config struct {
	Listen    string
	DataDir   string
	MediaRoot string

	// The Jellyfin-compatible API (Swiftfin, Infuse) also gets a listener
	// of its own (VAULT_JELLYFIN_LISTEN, ":8096", the port those apps
	// expect; "off" disables it), and reports JellyfinVersion
	// (VAULT_JELLYFIN_VERSION) as the server version.
	JellyfinListen  string
	JellyfinVersion string

	// Relative to MediaRoot.
	MoviesDir     string
	SeriesDir     string
	MusicDir      string
	BooksDir      string
	EbooksDir     string // VAULT_EBOOKS_DIR
	AudiobooksDir string // VAULT_AUDIOBOOKS_DIR
	ComicsDir     string // VAULT_COMICS_DIR (manga and comics live together)

	TMDBKey       string
	TMDBBaseURL   string
	TMDBImageBase string
	// Music and book metadata sources; "off" disables one (e2e sets all to
	// off). VAULT_OPENLIBRARY_BASEURL=off also disables its covers.
	MusicBrainzURL       string // VAULT_MUSICBRAINZ_BASEURL
	CoverArtURL          string // VAULT_COVERART_BASEURL
	OpenLibraryURL       string // VAULT_OPENLIBRARY_BASEURL
	OpenLibraryCoversURL string // VAULT_OPENLIBRARY_COVERS_BASEURL
	// OpenSubtitles (automatic subtitles): off without a key; "off" as the
	// base URL also disables it. User and password raise the daily quota.
	OpenSubtitlesKey      string // VAULT_OPENSUBTITLES_KEY
	OpenSubtitlesUser     string // VAULT_OPENSUBTITLES_USER
	OpenSubtitlesPassword string // VAULT_OPENSUBTITLES_PASSWORD
	OpenSubtitlesURL      string // VAULT_OPENSUBTITLES_BASEURL
	FFmpeg, FFprobe       string
	HWAccel               string // auto|qsv|videotoolbox|none
	// TranscodeDir holds HLS session segments (VAULT_TRANSCODE_DIR; a
	// relative path is under MediaRoot; default DataDir/hls). Point it at
	// the media disk: a session keeps minutes of video there.
	TranscodeDir string
	// TranscodeMaxBytes is the total size of all HLS sessions; above it the
	// sessions nobody watches are stopped (VAULT_TRANSCODE_MAX_BYTES).
	TranscodeMaxBytes int64
	// DeviceCopyDir holds the native app's offline copies of videos and the
	// comic page zips (VAULT_DEVICE_COPY_DIR; a relative path is under
	// MediaRoot; default TranscodeDir/device); DeviceCopyMaxBytes is their
	// total size (VAULT_DEVICE_COPY_MAX_BYTES, 50 GiB): above it the least
	// recently downloaded ones are deleted.
	DeviceCopyDir      string
	DeviceCopyMaxBytes int64
	// ChannelsMax is how many «ТВ-каналы» may stream at once, one live
	// encoder each, in TranscodeDir/live (VAULT_CHANNELS_MAX, default 3;
	// 0 turns channel streaming off).
	ChannelsMax int
	// IPTV opens the channels to IPTV players: M3U and XMLTV on token URLs
	// (VAULT_IPTV=on; default off — channels are watched in the apps).
	IPTV bool

	// TrustedNets (VAULT_TRUSTED_NETS) are extra home networks on top of
	// loopback, RFC1918 and IPv6 ULA/link-local; TrustedProxies
	// (VAULT_TRUSTED_PROXIES) are reverse proxies whose X-Forwarded-For is
	// believed (the Nginx Proxy Manager host). Comma-separated CIDRs or IPs.
	TrustedNets    []netip.Prefix
	TrustedProxies []netip.Prefix

	// RepackTS (VAULT_REPACK_TS, on unless "off"): films and episodes in
	// MPEG-TS (.ts, .m2ts, .mts) are repacked into Matroska without
	// re-encoding after a scan, for fast seeks (library.Repacker).
	RepackTS bool

	// The media servers' data for «Переезд», mounted read-only under
	// ImportDir (VAULT_IMPORT_DIR, /import): jellyfin/jellyfin.db,
	// kavita/kavita.db, audiobookshelf/absdatabase.sqlite (the sources build
	// adds its own old services there).
	ImportDir string
	// ImportPaths rewrites the paths Kavita and Audiobookshelf keep
	// (VAULT_IMPORT_PATHS, "from=to,from=to"; the longest prefix wins).
	// Default: /books/ebooks, /books/manga, /audiobooks, /ebooks → the
	// book folders.
	ImportPaths mediaimport.PathMap

	// LibraryOnly: the server serves its library alone. Always true in the
	// core build (built without -tags sources); in the sources build it is
	// VAULT_LIBRARY_ONLY (see config_full.go).
	LibraryOnly bool

	// Acquisition holds the settings of the sources build's features that
	// bring titles from outside; an empty struct in the core build.
	Acquisition

	// LRCLibURL (VAULT_LRCLIB_URL) is where song words are looked up
	// online (default https://lrclib.net); "" (VAULT_LRCLIB_URL=off): only
	// the words in the files. A library-only server defaults to off.
	LRCLibURL string

	// WikidataURL (VAULT_WIKIDATA_BASEURL, "off": none, "on":
	// DefaultWikidataURL): Rotten Tomatoes scores of movies and series, and
	// in the sources build the second book catalog of «Найти и добавить»
	// (Russian titles). A library-only server (the one App Store users
	// run) defaults to off, as for LRCLIB: it sends the IMDb ids of the
	// library to a third party.
	WikidataURL string
	// IMDbRatingsURL (VAULT_IMDB_RATINGS_URL) is the IMDb dataset
	// title.ratings.tsv.gz the IMDb ratings come from ("" = none; "off"
	// turns it off, "on" picks DefaultIMDbRatingsURL). The dataset is for
	// personal, non-commercial use: a library-only server (the one App
	// Store users run) defaults to off, as for LRCLIB.
	IMDbRatingsURL string
	// SegmentsOnline (VAULT_SEGMENTS_ONLINE "on"/"off") asks TheIntroDB and
	// IntroDB where the recap, intro and credits of the library's episodes
	// and films are (their TMDB/IMDb ids with the season and episode). A
	// library-only server (the one App Store users run) defaults to off, as
	// for LRCLIB: chapters and the server's own analysis still work.
	SegmentsOnline bool
	// PublicURL is how browsers reach this server ("https://vault.example.org",
	// VAULT_PUBLIC_URL, no trailing slash): the links of notification
	// letters and (sources build) the library import's OAuth redirects.
	// "": letters carry no links.
	PublicURL string
}

// DefaultIMDbRatingsURL is IMDb's own copy of the ratings dataset.
const DefaultIMDbRatingsURL = "https://datasets.imdbws.com/title.ratings.tsv.gz"

// DefaultWikidataURL is the public Wikidata.
const DefaultWikidataURL = "https://www.wikidata.org"

// DefaultLRCLibURL is the public LRCLIB.
const DefaultLRCLibURL = "https://lrclib.net"

// ParsePrefixes reads a comma-separated list of CIDRs or bare IPs.
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// defaultMediaRoot picks the media root when VAULT_MEDIA_ROOT is unset:
// /media when it exists (the Docker mount), else ./media (made absolute).
func defaultMediaRoot(stat func(path string) (isDir, nonEmpty bool)) string {
	if dir, _ := stat("/media"); dir {
		return "/media"
	}
	if abs, err := filepath.Abs("media"); err == nil {
		return abs
	}
	return "media"
}

func statDir(path string) (isDir, nonEmpty bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.IsDir() {
		return false, false
	}
	names, _ := f.Readdirnames(1)
	return true, len(names) > 0
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		Listen:                env("VAULT_LISTEN", ":8420"),
		JellyfinListen:        env("VAULT_JELLYFIN_LISTEN", ":8096"),
		JellyfinVersion:       env("VAULT_JELLYFIN_VERSION", "12.1.0"),
		DataDir:               env("VAULT_DATA_DIR", "./data"),
		MediaRoot:             os.Getenv("VAULT_MEDIA_ROOT"),
		MoviesDir:             env("VAULT_MOVIES_DIR", "media/movies"),
		SeriesDir:             env("VAULT_SERIES_DIR", "media/series"),
		MusicDir:              env("VAULT_MUSIC_DIR", "media/music"),
		BooksDir:              env("VAULT_BOOKS_DIR", "media/books"),
		TMDBKey:               os.Getenv("VAULT_TMDB_KEY"),
		TMDBBaseURL:           env("VAULT_TMDB_BASEURL", "https://api.themoviedb.org/3"),
		TMDBImageBase:         env("VAULT_TMDB_IMAGEBASE", "https://image.tmdb.org/t/p"),
		MusicBrainzURL:        env("VAULT_MUSICBRAINZ_BASEURL", "https://musicbrainz.org/ws/2"),
		CoverArtURL:           env("VAULT_COVERART_BASEURL", "https://coverartarchive.org"),
		OpenLibraryURL:        env("VAULT_OPENLIBRARY_BASEURL", "https://openlibrary.org"),
		OpenLibraryCoversURL:  env("VAULT_OPENLIBRARY_COVERS_BASEURL", "https://covers.openlibrary.org"),
		OpenSubtitlesKey:      os.Getenv("VAULT_OPENSUBTITLES_KEY"),
		OpenSubtitlesUser:     os.Getenv("VAULT_OPENSUBTITLES_USER"),
		OpenSubtitlesPassword: os.Getenv("VAULT_OPENSUBTITLES_PASSWORD"),
		OpenSubtitlesURL:      env("VAULT_OPENSUBTITLES_BASEURL", "https://api.opensubtitles.com/api/v1"),
		FFmpeg:                env("VAULT_FFMPEG", "ffmpeg"),
		FFprobe:               env("VAULT_FFPROBE", "ffprobe"),
		HWAccel:               env("VAULT_HWACCEL", "auto"),
	}
	if c.MediaRoot == "" {
		c.MediaRoot = defaultMediaRoot(statDir)
	}
	// the book kinds live under VAULT_BOOKS_DIR unless set one by one
	c.EbooksDir = env("VAULT_EBOOKS_DIR", filepath.Join(c.BooksDir, "ebooks"))
	c.AudiobooksDir = env("VAULT_AUDIOBOOKS_DIR", filepath.Join(c.BooksDir, "audiobooks"))
	c.ComicsDir = env("VAULT_COMICS_DIR", filepath.Join(c.BooksDir, "manga"))
	c.TranscodeDir = filepath.Join(c.DataDir, "hls")
	if v := os.Getenv("VAULT_TRANSCODE_DIR"); v != "" {
		c.TranscodeDir = c.Abs(v)
	}
	c.TranscodeMaxBytes = 20 << 30
	if v := os.Getenv("VAULT_TRANSCODE_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("VAULT_TRANSCODE_MAX_BYTES: %w", err)
		}
		c.TranscodeMaxBytes = n
	}
	c.DeviceCopyDir = filepath.Join(c.TranscodeDir, "device")
	if v := os.Getenv("VAULT_DEVICE_COPY_DIR"); v != "" {
		c.DeviceCopyDir = c.Abs(v)
	}
	c.DeviceCopyMaxBytes = 50 << 30
	if v := os.Getenv("VAULT_DEVICE_COPY_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("VAULT_DEVICE_COPY_MAX_BYTES: %w", err)
		}
		c.DeviceCopyMaxBytes = n
	}
	c.ChannelsMax = 3
	switch strings.ToLower(os.Getenv("VAULT_IPTV")) {
	case "", "off", "0", "false", "no":
	case "on", "1", "true", "yes":
		c.IPTV = true
	default:
		return c, fmt.Errorf("VAULT_IPTV: %q is not on or off", os.Getenv("VAULT_IPTV"))
	}
	if v := os.Getenv("VAULT_CHANNELS_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("VAULT_CHANNELS_MAX: %q is not a count", v)
		}
		c.ChannelsMax = n
	}
	var err error
	if c.TrustedNets, err = ParsePrefixes(os.Getenv("VAULT_TRUSTED_NETS")); err != nil {
		return c, fmt.Errorf("VAULT_TRUSTED_NETS: %w", err)
	}
	if c.TrustedProxies, err = ParsePrefixes(os.Getenv("VAULT_TRUSTED_PROXIES")); err != nil {
		return c, fmt.Errorf("VAULT_TRUSTED_PROXIES: %w", err)
	}
	c.ImportDir = env("VAULT_IMPORT_DIR", "/import")
	if v := os.Getenv("VAULT_IMPORT_PATHS"); v != "" {
		if c.ImportPaths, err = mediaimport.ParsePathMap(v); err != nil {
			return c, fmt.Errorf("VAULT_IMPORT_PATHS: %w", err)
		}
	} else {
		// Kavita and Audiobookshelf mount the book folders here (map-context §3)
		c.ImportPaths = mediaimport.PathMap{
			{From: "/books/ebooks", To: c.Abs(c.EbooksDir)},
			{From: "/books/manga", To: c.Abs(c.ComicsDir)},
			{From: "/audiobooks", To: c.Abs(c.AudiobooksDir)},
			{From: "/ebooks", To: c.Abs(c.EbooksDir)},
		}
	}
	if err := c.loadAcquisition(); err != nil {
		return c, err
	}
	switch v := strings.TrimSpace(os.Getenv("VAULT_LRCLIB_URL")); {
	case strings.EqualFold(v, "off"):
		c.LRCLibURL = ""
	case v != "":
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return c, errors.New("VAULT_LRCLIB_URL: неверный адрес")
		}
		c.LRCLibURL = strings.TrimRight(v, "/")
	case !c.LibraryOnly:
		c.LRCLibURL = DefaultLRCLibURL
	}
	switch v := strings.TrimSpace(os.Getenv("VAULT_WIKIDATA_BASEURL")); {
	case strings.EqualFold(v, "off"):
		c.WikidataURL = ""
	case strings.EqualFold(v, "on"):
		c.WikidataURL = DefaultWikidataURL
	case v != "":
		c.WikidataURL = v
	case !c.LibraryOnly:
		c.WikidataURL = DefaultWikidataURL
	}
	switch v := strings.TrimSpace(os.Getenv("VAULT_IMDB_RATINGS_URL")); {
	case strings.EqualFold(v, "off"):
		c.IMDbRatingsURL = ""
	case strings.EqualFold(v, "on"):
		c.IMDbRatingsURL = DefaultIMDbRatingsURL
	case v != "":
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return c, errors.New("VAULT_IMDB_RATINGS_URL: неверный адрес")
		}
		c.IMDbRatingsURL = v
	case !c.LibraryOnly:
		c.IMDbRatingsURL = DefaultIMDbRatingsURL
	}
	c.PublicURL = strings.TrimRight(strings.TrimSpace(os.Getenv("VAULT_PUBLIC_URL")), "/")
	c.SegmentsOnline = !c.LibraryOnly
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("VAULT_SEGMENTS_ONLINE"))); v {
	case "":
	case "on", "1", "true", "yes":
		c.SegmentsOnline = true
	case "off", "0", "false", "no":
		c.SegmentsOnline = false
	default:
		return c, fmt.Errorf("VAULT_SEGMENTS_ONLINE: unknown value %q", v)
	}
	switch c.HWAccel {
	case "auto", "qsv", "videotoolbox", "none":
	default:
		return c, fmt.Errorf("VAULT_HWACCEL: unknown value %q", c.HWAccel)
	}
	return c, nil
}

// Abs resolves a path relative to MediaRoot (absolute paths are returned as is).
func (c Config) Abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(c.MediaRoot, rel)
}
