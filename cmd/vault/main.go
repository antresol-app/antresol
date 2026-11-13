package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/jellyfin"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/loudness"
	"github.com/antresol-app/antresol/internal/lyrics"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/mediaimport"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/opds"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/releaseprefs"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/antresol-app/antresol/internal/segments"
	"github.com/antresol-app/antresol/internal/subsonic"
	"github.com/antresol-app/antresol/internal/trickplay"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/antresol-app/antresol/internal/voclean"
	"github.com/antresol-app/antresol/internal/vofit"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// core is what every build serves: the library, its playback and the
// client protocols. The sources build adds its own features on top of it
// (wire_full.go); the core build has none (wire_library.go).
type core struct {
	cfg      config.Config
	ctx      context.Context
	db       *sql.DB
	repo     *library.Repo
	jobs     *library.Jobs
	images   *metadata.Images
	meta     metaSources
	bus      *events.Bus
	disk     *disk.Monitor
	progress *progress.Store
	// mediaImport is «Переезд»: the import from Jellyfin, Kavita and
	// Audiobookshelf (the sources build adds its own sources, wire_full.go).
	mediaImport *mediaimport.Service
	// playlists is «Импорт медиатеки»: playlists and favourites from files
	// (another build adds more, wire_full.go).
	playlists *playlistimport.Service
	// LibraryReady says whether the first scan is done (and why not).
	libraryReady func() (bool, string)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for _, h := range cfg.PathHints(nil) {
		log.Print(h)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "vault.db"))
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repo := library.NewRepo(database)
	jobs := &library.Jobs{
		Scanner: &library.Scanner{Repo: repo, MediaRoot: cfg.MediaRoot,
			Movies: cfg.Abs(cfg.MoviesDir), Series: cfg.Abs(cfg.SeriesDir), Music: cfg.Abs(cfg.MusicDir),
			Ebooks: cfg.Abs(cfg.EbooksDir), Audiobooks: cfg.Abs(cfg.AudiobooksDir), Comics: cfg.Abs(cfg.ComicsDir),
			ArtDir: filepath.Join(cfg.DataDir, "images"), ComicsCache: filepath.Join(cfg.DataDir, "comics-cache"),
			Chapters: func(ctx context.Context, path string) ([]library.ChapterInput, error) {
				chs, err := media.Chapters(ctx, cfg.FFprobe, path)
				out := make([]library.ChapterInput, len(chs))
				for i, c := range chs {
					out[i] = library.ChapterInput{Title: c.Title, StartMs: c.StartMs, EndMs: c.EndMs}
				}
				return out, err
			},
			// An unmounted media disk leaves an empty folder on the root
			// disk: scans do not prune while it is missing (another build runs the
			// same check).
			StorageOffline: storageOffline(cfg)},
		Probe: func(ctx context.Context, path string) (string, int64, error) {
			p, err := media.Probe(ctx, cfg.FFprobe, path)
			if err != nil {
				return "", 0, err
			}
			b, err := json.Marshal(p)
			return string(b), p.DurationMs, err
		},
	}
	images := &metadata.Images{Dir: filepath.Join(cfg.DataDir, "images"), UserAgent: version.UserAgent}
	metaSrc := buildEnrichers(cfg, repo, images, slog.Default(), subtitleDeps{DB: database, DataDir: cfg.DataDir,
		Settings: func(ctx context.Context) ([]string, bool) {
			s, err := api.LoadAppSettings(ctx, database, api.AppSettings{})
			if err != nil {
				log.Printf("subtitles: settings: %v", err)
			}
			return s.SubtitleSettings()
		}})
	jobs.Enrichers, jobs.SlowEnrichers = metaSrc.Fast, metaSrc.Slow

	hw := media.DetectHW(cfg.HWAccel)
	// Session segments live in VAULT_TRANSCODE_DIR (the media disk in
	// compose, not the config volume); dirs left by a crash are wiped here.
	// Audio transcodes have their own 4 slots (not HLS's); Subsonic shares them.
	audioTr := &media.AudioTranscoder{FFmpeg: cfg.FFmpeg, Limit: 4}
	hls := &media.HLSManager{FFmpeg: cfg.FFmpeg, WorkDir: cfg.TranscodeDir, HW: hw, MaxBytes: cfg.TranscodeMaxBytes}
	defer hls.Close()
	// The native app's offline copies (MP4) and comic page zips; copies run
	// one at a time, beside the HLS segments on the media disk by default.
	deviceCopies := &media.DeviceCopies{Dir: cfg.DeviceCopyDir, MaxBytes: cfg.DeviceCopyMaxBytes, FFmpeg: cfg.FFmpeg, HW: hw}
	deviceCopies.Start() // deletes partial copies left by a crash
	defer deviceCopies.Close()

	bus := &events.Bus{}
	progStore := progress.New(database)
	// the media disk's fill level (disk.warning, GET /api/disk); a
	// library-only server shows nobody its disk
	mon := &disk.Monitor{Root: cfg.MediaRoot, Offline: storageOffline(cfg), Bus: bus}
	libraryReady := func() (bool, string) {
		// matching by path needs the library: wait for the first scan
		st := jobs.Status()
		if !st.Running && !st.FinishedAt.IsZero() {
			return true, ""
		}
		return false, "Медиатека ещё сканируется — подождите"
	}
	// «Переезд»: always there; a service whose files are not mounted under
	// VAULT_IMPORT_DIR is «Не найден». (Profiles are set once they exist.)
	jfDB, kvDB, absDB := mediaimport.DefaultPaths(cfg.ImportDir)
	mediaImport := mediaimport.NewService(&mediaimport.Service{
		Library: repo, Progress: progStore, Jellyfin: jfDB, Kavita: kvDB, ABS: absDB, ImportPaths: cfg.ImportPaths,
		ReportDir: filepath.Join(cfg.DataDir, "migrate"), TmpDir: filepath.Join(cfg.DataDir, "tmp", "migrate"),
		BaseCtx: ctx, Bus: bus, Log: slog.Default().With("component", "migrate"), LibraryReady: libraryReady,
	})
	// «Импорт медиатеки»: the preview shows the disk, except on a
	// library-only server (nobody's business there)
	importDisk := mon.Usage
	if cfg.LibraryOnly {
		importDisk = nil
	}
	playlists := newPlaylistImport(database, bus, repo, metaSrc.MusicBrainz, importDisk)
	c := core{cfg: cfg, ctx: ctx, db: database, repo: repo, jobs: jobs, images: images, meta: metaSrc, bus: bus, disk: mon, progress: progStore,
		mediaImport: mediaImport, playlists: playlists, libraryReady: libraryReady}
	ext, err := newExtras(c)
	if err != nil {
		return err
	}
	defer ext.close()

	// Saved settings apply live: «Язык метаданных» (and the sources build's own).
	settingsDefaults := ext.settingsDefaults()
	var trickWorker *trickplay.Worker // set below; Kick is nil-safe
	var chans *channels.Service       // set below
	metaLang := ""
	applySettings := func(s api.AppSettings) {
		ext.applySettings(s)
		// «Превью при перемотке» turned on: the queue starts now
		if s.Trickplay {
			trickWorker.Kick()
		}
		// «Язык метаданных»: the language of future scans and enrichment
		if metaSrc.TMDB != nil {
			metaSrc.TMDB.SetLanguage(i18n.TMDB(s.MetadataLanguageOrDefault()))
		}
		// and of the live channels' audio and subtitle names
		metaLang = s.MetadataLanguageOrDefault()
		if chans != nil {
			chans.SetLanguage(metaLang)
		}
	}
	if saved, err := api.LoadAppSettings(ctx, database, settingsDefaults); err != nil {
		log.Printf("settings: %v (using defaults)", err)
	} else {
		applySettings(saved)
	}

	// File browser.
	// a library-only server: no disk fill level (the demo's disk is
	// nobody's business)
	fileRoots := files.DefaultRoots(cfg)
	if cfg.LibraryOnly {
		fileRoots = files.LibraryOnlyRoots(fileRoots)
	}
	fileCfg := files.ServiceConfig{
		NoDiskUsage: cfg.LibraryOnly,
		Sandbox:     files.NewSandbox(cfg.MediaRoot, fileRoots),
		DB:          database, Library: repo, DataDir: cfg.DataDir, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
		Changed: func(...string) { jobs.Trigger() }, // the scanner covers all video roots
		// deleting a library item: only inside the scanner's folders, never
		// while the media disk is away (read at the call)
		LibraryRoots: []string{jobs.Scanner.Movies, jobs.Scanner.Series, jobs.Scanner.Music,
			jobs.Scanner.Ebooks, jobs.Scanner.Audiobooks, jobs.Scanner.Comics},
		Offline: func() string {
			if f := jobs.Scanner.StorageOffline; f != nil {
				return f()
			}
			return ""
		},
		// no scan runs (nor brings rows back) while an item's files move
		Hold: jobs.Scanner.Hold, // waits for a running scan a few seconds at most
	}
	ext.fileHooks(&fileCfg)
	fileSvc := files.NewService(fileCfg)

	if len(cfg.TrustedProxies) == 0 {
		log.Printf("auth: WARNING! VAULT_TRUSTED_PROXIES is not set. Behind a reverse proxy (Nginx Proxy Manager) every internet visitor looks like the proxy: one stranger's 5 wrong passwords lock password logins for everyone, and remote caps do not apply. Set VAULT_TRUSTED_PROXIES to the proxy address.")
	}

	// One login throttle for the web, Subsonic and OPDS: guessing an app
	// password also locks the web login from that address.
	users, network, throttle := auth.NewStore(database), &auth.Network{Trusted: cfg.TrustedNets, Proxies: cfg.TrustedProxies}, auth.NewThrottle()
	musicStore := music.New(database)
	// apps recognise this server by its id (GET /api/health)
	serverID, err := api.LoadServerID(ctx, database)
	if err != nil {
		return fmt.Errorf("server id: %w", err)
	}
	// media keys (?mk=) let AVPlayer and AirPlay fetch the app's media
	mkSecret, err := auth.LoadOrCreateSecret(ctx, database, "media_key_secret")
	if err != nil {
		return fmt.Errorf("media key secret: %w", err)
	}
	sessions := auth.NewSessions(database)
	// «Уведомления на почту»: digests from the Kindle mail account; the
	// links of the letters are signed with their own secret
	notifySecret, err := auth.LoadOrCreateSecret(ctx, database, "notify_secret")
	if err != nil {
		return fmt.Errorf("notify secret: %w", err)
	}
	notifier := &notify.Service{DB: database, Users: users, Secret: notifySecret, PublicURL: cfg.PublicURL,
		Mail: api.NotifyMail(func(ctx context.Context) (api.AppSettings, error) {
			return api.LoadAppSettings(ctx, database, settingsDefaults)
		}),
		Log: slog.Default().With("component", "notify")}
	// a kid's request whose title reached the library is news for the kid
	notifier.Collectors = append(notifier.Collectors, notifier.RequestsInLibrary(repo.FindTitleID))
	ext.notifyHooks(notifier)
	// .ts / .m2ts films become .mkv after a scan (seeks in seconds, not
	// half a minute); its own worker, so a long repack never holds a scan
	repacker := &library.Repacker{DB: database, Scanner: jobs.Scanner, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
		Keep: 7 * 24 * time.Hour}
	// song words of the library's tracks, fetched in the background and
	// kept (LRCLIB, one request a second; off with VAULT_LRCLIB_URL=off)
	var lyricsAuto *lyrics.Auto
	if cfg.LRCLibURL != "" {
		bg := lyrics.NewAt(cfg.LRCLibURL, version.UserAgent)
		bg.Limit = &metadata.Limiter{Interval: time.Second}
		lyricsAuto = &lyrics.Auto{Client: bg, Repo: repo, Log: slog.Default()}
	}
	// recap, intro and credits of the library's videos: chapters, the
	// online databases (unless VAULT_SEGMENTS_ONLINE=off, the default of a
	// library-only server) and the server's own analysis, a season at a
	// time in the background, yielding the disk to playback
	var segOnline *segments.Online
	if cfg.SegmentsOnline {
		segOnline = segments.NewOnline(version.UserAgent)
	}
	// reads of the media disk (stream and HLS segment reads, downloads,
	// an HLS ffmpeg running) hold the analysis: it reads only after 2 min
	// of quiet; heartbeats and progress saves of a paused player do not
	playback := &media.Activity{}
	// and the web app's page loads, for a few seconds (browseQuiet): the
	// background readers below pause (SIGSTOP) while someone browses
	browsing := &media.Activity{}
	segWorker := &segments.Worker{Store: segments.Store{DB: database}, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
		Online: segOnline, Log: slog.Default(), CacheDir: filepath.Join(cfg.DataDir, "segments"),
		Busy: func() bool {
			return hls.Reading(segments.PlaybackIdle) || playback.Reading(segments.PlaybackIdle) || browsing.Reading(browseQuiet)
		}}
	// scrub previews of the library's videos (DataDir/trickplay), a file at
	// a time behind the same quiet as the analysis, at the lowest priority;
	// «Превью при перемотке» (on by default) turns them off and deletes them,
	// «Место для превью» bounds them; a file without a keyframe index
	// (Matroska without Cues) is read in one pass, the index cached beside
	// the remux's
	keyIndexes := media.KeyIndexCache{Dir: filepath.Join(cfg.TranscodeDir, "keyindex")}
	trickWorker = &trickplay.Worker{DB: database, Dir: filepath.Join(cfg.DataDir, "trickplay"), FFmpeg: cfg.FFmpeg,
		Log: slog.Default(), Busy: segWorker.Busy,
		Offline: func() string {
			if f := jobs.Scanner.StorageOffline; f != nil {
				return f()
			}
			return ""
		},
		KeyIndex: func(path string) error { _, _, err := keyIndexes.Get(path); return err },
		Settings: func(ctx context.Context) (bool, int64) {
			s, err := api.LoadAppSettings(ctx, database, api.AppSettings{})
			if err != nil {
				log.Printf("trickplay: settings: %v", err)
			}
			return s.Trickplay, s.TrickplayBudgetBytes()
		}}
	// «Выравнивание громкости»: the loudness of the tracks without
	// ReplayGain tags, a track at a time behind the same quiet, at the
	// lowest priority; album gains from all of an album's tracks
	loudWorker := &loudness.Worker{DB: database, FFmpeg: cfg.FFmpeg, Log: slog.Default(), Busy: segWorker.Busy}
	// «Очищено»: voice-overs with the original speech subtracted
	// (DataDir/audio), a file at a time, what is being watched first; at
	// the lowest priority; fresh downloads and next episodes even while
	// something plays, the backlog behind the same quiet as the analysis;
	// held while the media disk is away or DataDir's disk is short of room
	vocWorker := &voclean.Worker{DB: database, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe, DataDir: cfg.DataDir, Log: slog.Default(),
		Studios: releaseprefs.Store{DB: database}.Studios, Busy: segWorker.Busy,
		Disk: func() bool {
			if f := jobs.Scanner.StorageOffline; f != nil && f() != "" {
				return false
			}
			return spaceFor(cfg.DataDir)
		}}
	// the fit of the voice-overs «Найти озвучку…» placed beside their videos
	// (vofit), even while something plays, at the lowest priority; a
	// release's own tracks (stage A) are never measured
	fitWorker := &vofit.Worker{DB: database, FFmpeg: cfg.FFmpeg, Log: slog.Default(), Busy: segWorker.Busy,
		Fitted: vocWorker.Kick} // a fitted voice-over is cleaned like the file's own
	runFit := ext.voiceOverFit(fitWorker) // the sources build only (stage B)
	// «ТВ-каналы»: one live encoder per watched channel, beside the HLS
	// sessions on the media disk; schedules kept 48 h ahead
	var live *channels.Live
	if cfg.ChannelsMax > 0 {
		live = &channels.Live{FFmpeg: cfg.FFmpeg, HW: hw, Dir: filepath.Join(cfg.TranscodeDir, "live"), Max: cfg.ChannelsMax}
		defer live.Close()
	}
	chans = channels.New(database, live)
	chans.SetLanguage(metaLang)
	// channels of picked folders and the editor's folder browse
	chans.SetRoots(channels.LibraryRoots(jobs.Scanner.Movies, jobs.Scanner.Series))
	chans.Probe = func(ctx context.Context, path string) (media.ProbeResult, error) {
		return media.Probe(ctx, cfg.FFprobe, path)
	}
	// generated channels follow the library (new files need their
	// durations), vanished files leave the schedules
	jobs.AfterProbe = func(context.Context) { chans.Kick() }
	jobs.AfterScan = func(ctx context.Context) {
		ext.afterScan(ctx)
		// new episodes and titles for the letters (enriched by now)
		if err := notifier.Collect(ctx); err != nil && ctx.Err() == nil {
			log.Printf("notify: collect: %v", err)
		}
		lyricsAuto.Kick() // new tracks first
		segWorker.Kick()  // new episodes and films
		trickWorker.Kick()
		loudWorker.Kick() // new tracks, tags read again
		vocWorker.Kick()  // new films and episodes
		if runFit {
			fitWorker.Kick() // a video replaced: its voice-overs' fits are stale, fitted again now
		}
		if cfg.RepackTS {
			repacker.Kick()
		}
	}
	var genreNamer api.GenreNamer // a nil interface without a TMDB key, never a typed nil
	var translator api.Translator // likewise
	var related *tmdb.Related     // «Похожие» and «Коллекция»; nil without a key
	var artwork *tmdb.Photos      // posters and logos in the viewer's language
	if metaSrc.TMDB != nil {
		related = &tmdb.Related{Client: metaSrc.TMDB, DB: database}
		defer related.Close() // its background refreshes end before the database closes
		genreNamer = metaSrc.TMDB
		// item pages in a language other than «Язык метаданных»
		artwork = &tmdb.Photos{Client: metaSrc.TMDB, Images: images, Size: tmdb.ArtSize}
		tr := &tmdb.Translations{Client: metaSrc.TMDB, DB: database, Art: artwork}
		defer tr.Close() // before the database closes (defers run in reverse)
		translator = tr
	}
	// «Экранное время» of kid profiles: one counter for the web, Subsonic
	// and Jellyfin (the day is the server's, TZ)
	screenTime := screentime.New(database)
	deps := api.Deps{
		ScreenTime: screenTime,
		Users:      users, Sessions: sessions, Network: network, Throttle: throttle,
		MediaKeys: auth.NewMediaKeys(mkSecret, sessions),
		Library:   repo, Progress: progStore, Jobs: jobs, HLS: hls,
		AudioTranscoder: audioTr, DeviceCopies: deviceCopies,
		Music: musicStore, ComicsCache: filepath.Join(cfg.DataDir, "comics-cache"),
		DataDir: cfg.DataDir, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
		Files:  fileSvc,
		Events: bus,
		DB:     database, SettingsDefaults: settingsDefaults, ApplySettings: applySettings,
		Subtitles:    metaSrc.Subtitles,
		Genres:       genreNamer,
		Translations: translator,
		ServerID:     serverID,
		LibraryOnly:  cfg.LibraryOnly,
		Segments:     segWorker,
		Notify:       notifier,
		Trickplay:    trickWorker,
		Channels:     chans,
		IPTV:         cfg.IPTV,
	}
	// external IPTV playlists and guides in «ТВ» (added by an admin);
	// their secrets sealed with the server's key
	seal, err := iptv.NewSealer(ctx, database)
	if err != nil {
		return err
	}
	extTV := &iptv.Service{Store: &iptv.Store{DB: database}, Channels: chans.Store, Fetch: iptv.NewFetcher(), Seal: seal,
		ScreenLimited: func(ctx context.Context, uid int64) bool {
			l, err := screenTime.Limits(ctx, uid)
			return err == nil && l.Any()
		}}
	deps.ExtIPTV = extTV
	// kids' requests: titles from TMDB, answered by the library (the full
	// build's own catalog replaces this in ext.apiDeps)
	if metaSrc.TMDB != nil {
		deps.Requests = api.TMDBRequests(metaSrc.TMDB, repo)
	}
	if !cfg.LibraryOnly {
		deps.DiskUsage = mon.Usage // another build's own replaces it in ext.apiDeps
	}
	if related != nil { // never a typed nil in the interface
		deps.Related = related
	}
	// IMDb and Rotten Tomatoes ratings of title pages
	if rs := buildRatings(cfg, database, metaSrc.Wikidata, slog.Default()); rs != nil {
		deps.Ratings = rs
		if rs.IMDb != nil {
			defer rs.IMDb.Close()
		}
	}
	// song words online (LRCLIB), unless VAULT_LRCLIB_URL=off (the default
	// of a library-only server)
	if cfg.LRCLibURL != "" {
		deps.Lyrics = lyrics.NewAt(cfg.LRCLibURL, version.UserAgent)
	}
	// cast photos from the image cache (the enricher downloads them), and
	// posters and logos in the viewer's language
	if metaSrc.TMDB != nil {
		deps.CastPhotos = &tmdb.Photos{Client: metaSrc.TMDB, Images: images}
		deps.Artwork = artwork
	}
	mediaImport.Profiles = users
	deps.MediaImport = mediaImport
	deps.PlaylistImport = playlists
	if err := ext.apiDeps(&deps, users); err != nil {
		return err
	}
	notifier.RequestsOpen = deps.RequestsOpen // «Просьбы» in the letters wherever requests are open
	webAPI, player := api.NewRouterWithPlayer(deps)
	subsonicAPI := &subsonic.Server{Users: users, Library: repo, Music: musicStore, Network: network, Throttle: throttle,
		Version: version.Version, ImageDir: filepath.Join(cfg.DataDir, "images"),
		Audio: audioserve.Deps{
			Transcoder: audioTr, // the same 4 slots as the web player
			IsRemote:   func(r *http.Request) bool { return !network.IsTrusted(r) },
			RemoteKbps: func(ctx context.Context) int {
				s, err := api.LoadAppSettings(ctx, database, settingsDefaults)
				if err != nil {
					log.Printf("subsonic: settings for remote music: %v", err)
				}
				return s.RemoteMusicKbps()
			},
		}}
	opdsCatalog := &opds.Handler{Library: repo, Users: users, Network: network, Throttle: throttle,
		ImageDir: filepath.Join(cfg.DataDir, "images"), ScreenTime: screenTime}
	// Swiftfin and Infuse: the Jellyfin API, on the main listener (known
	// path prefixes, e.g. behind the proxy) and on a listener of its own at
	// the port those apps expect (VAULT_JELLYFIN_LISTEN, "off": none).
	jellyfinServer := &jellyfin.Server{Users: users, Tokens: auth.NewAppTokens(database), Network: network, Throttle: throttle,
		DB: database, Version: cfg.JellyfinVersion, Log: slog.Default(),
		Library: repo, Progress: progress.New(database), ImageDir: filepath.Join(cfg.DataDir, "images"), DataDir: cfg.DataDir}
	if related != nil {
		jellyfinServer.Similar = related
	}
	if player != nil {
		jellyfinServer.Player = player // playback through the web player's HLS sessions and subtitle jobs
	}
	jellyfinServer.Activity = playback // its media reads, counted once authorised
	jellyfinServer.ScreenTime = screenTime
	subsonicAPI.ScreenTime = screenTime
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           trackPlayback(trackBrowsing(buildMux(webAPI, subsonicAPI, opdsCatalog, jellyfinServer), browsing), playback),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Open /api/events streams (one per tab) end when shutdown starts;
	// otherwise Shutdown waits for them until its timeout.
	srv.RegisterOnShutdown(bus.Close)
	servers := []*http.Server{srv}
	if cfg.JellyfinListen != "off" {
		servers = append(servers, &http.Server{
			Addr:              cfg.JellyfinListen,
			Handler:           trackPlayback(jellyfinServer, playback),
			ReadHeaderTimeout: srv.ReadHeaderTimeout, // the same timeouts as the main listener
			ReadTimeout:       srv.ReadTimeout,
			WriteTimeout:      srv.WriteTimeout,
			IdleTimeout:       srv.IdleTimeout,
		})
	}

	var wg sync.WaitGroup
	// the query planner's statistics follow the library as it grows
	wg.Add(1)
	go func() { defer wg.Done(); db.OptimizeEvery(ctx, database, 6*time.Hour) }()
	wg.Add(1)
	go func() { defer wg.Done(); jobs.Run(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); chans.Run(ctx, 10*time.Minute) }()
	wg.Add(1)
	go func() { defer wg.Done(); extTV.Run(ctx) }()
	if !cfg.LibraryOnly {
		wg.Add(1)
		go func() { defer wg.Done(); mon.Run(ctx, 30*time.Second) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); repacker.Run(ctx) }()
	if lyricsAuto != nil {
		wg.Add(1)
		go func() { defer wg.Done(); lyricsAuto.Run(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); segWorker.Run(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); notifier.Run(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); trickWorker.Run(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); loudWorker.Run(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); vocWorker.Run(ctx) }()
	if runFit {
		wg.Add(1)
		go func() { defer wg.Done(); fitWorker.Run(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); playlists.Run(ctx) }() // (the sources build's loops start below, not this one)
	ext.start(ctx, &wg)

	errc := make(chan error, len(servers))
	for i, s := range servers {
		if i == 0 {
			log.Printf("%s %s listening on %s (hw: %s)", serverName, version.Version, s.Addr, hw)
		}
		// the Jellyfin listener is optional: a port it cannot open (taken
		// by a Jellyfin still running) never stops Vault
		go runListener(s, i > 0, errc)
	}

	select {
	case err = <-errc:
		stop() // a server failed to start or died: stop the jobs and the other listener too
		for _, s := range servers {
			s.Close()
		}
	case <-ctx.Done():
		// Docker gives 30 s (stop_grace_period): ffmpeg first (a few
		// seconds at most, nothing to save), then 5 s for open requests,
		// then the jobs (and the sources build's own work).
		log.Printf("shutting down")
		hls.Close()
		deviceCopies.Close()
		err = shutdownAll(servers, 5*time.Second)
	}
	hls.Close() // (again, after a failed start) kill ffmpeg before the jobs and the db
	deviceCopies.Close()
	wg.Wait()  // the jobs (and the sources build's loops) end with ctx
	ext.wait() // the sources build's background work saves and stops
	mediaImport.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// runListener serves s and reports its end on errc. An optional listener
// (the Jellyfin port) that fails is logged and left off instead: Vault
// and its main listener go on (the Jellyfin API stays on the main
// listener's known paths).
func runListener(s *http.Server, optional bool, errc chan<- error) {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		if optional {
			log.Printf("jellyfin API: cannot listen on %s, continuing without that listener: %v", s.Addr, err)
			return
		}
		errc <- err
		return
	}
	if optional {
		log.Printf("jellyfin API listening on %s", s.Addr)
	}
	err = s.Serve(ln)
	if optional && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("jellyfin API: listener %s stopped, continuing without it: %v", s.Addr, err)
		return
	}
	errc <- err
}

// shutdownAll stops the listeners together, giving open requests up to
// grace; long-lived video streams past it are cut (still a normal stop).
func shutdownAll(servers []*http.Server, grace time.Duration) error {
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Shutdown(sctx)
			if errors.Is(err, context.DeadlineExceeded) {
				log.Printf("shutdown %s: timed out waiting for active streams, closing them", s.Addr)
				if cerr := s.Close(); cerr != nil {
					log.Printf("shutdown: close: %v", cerr)
				}
				err = nil
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
