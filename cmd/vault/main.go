package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/hex"
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
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
	"github.com/antresol-app/antresol/internal/accounts/myshows"
	"github.com/antresol-app/antresol/internal/accounts/shikimori"
	"github.com/antresol-app/antresol/internal/animemap"
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
	"github.com/antresol-app/antresol/internal/tracking"
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

	// the transcodes' GPU: auto tries the machine's with a test encode
	// each; «Настройки сервера → Перекодирование» overrides VAULT_HWACCEL*
	// (stored, applied at once to new sessions)
	accel := &media.Accel{FFmpeg: cfg.FFmpeg}
	hwDefaults := media.AccelConfig{Mode: cfg.HWAccel, Device: cfg.HWAccelDevice, Tonemap: true, HEVC: true}
	hwCfg, _, err := api.LoadTranscodingSettings(ctx, database, hwDefaults)
	if err != nil {
		log.Printf("transcoding settings: %v (using VAULT_HWACCEL)", err)
	}
	hwStatus := accel.Apply(ctx, hwCfg, false)
	hw := hwStatus.Mode
	for _, p := range hwStatus.Probes {
		if !p.OK {
			log.Printf("media: hardware test encode %s failed: %s", p.Mode, p.Error)
		}
	}
	// Session segments live in VAULT_TRANSCODE_DIR (the media disk in
	// compose, not the config volume); dirs left by a crash are wiped here.
	// Audio transcodes have their own 4 slots (not HLS's); Subsonic shares them.
	audioTr := &media.AudioTranscoder{FFmpeg: cfg.FFmpeg, Limit: 4}
	hls := &media.HLSManager{FFmpeg: cfg.FFmpeg, WorkDir: cfg.TranscodeDir, HW: hw, Accel: accel, MaxBytes: cfg.TranscodeMaxBytes}
	defer hls.Close()
	// The native app's offline copies (MP4) and comic page zips; copies run
	// one at a time, beside the HLS segments on the media disk by default.
	deviceCopies := &media.DeviceCopies{Dir: cfg.DeviceCopyDir, MaxBytes: cfg.DeviceCopyMaxBytes, FFmpeg: cfg.FFmpeg, HW: hw, Accel: accel}
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
		live = &channels.Live{FFmpeg: cfg.FFmpeg, HW: hw, Accel: accel, Dir: filepath.Join(cfg.TranscodeDir, "live"), Max: cfg.ChannelsMax}
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
	// ongoing tracking: the marked titles' episode lists (TMDB) and where
	// each episode can be got; new ones go to the profiles that marked them
	trackStore := &tracking.Store{DB: database}
	tracker := &tracking.Service{Store: trackStore, Scanners: []tracking.Scanner{tracking.NewLibraryScanner(repo)},
		Notify: notifier, Interest: trackStore.Interested, Log: slog.Default().With("component", "tracking"),
		Watched:     []tracking.WatchedFunc{tracking.LibraryWatched(repo, progStore)},
		WatchStates: []tracking.WatchStateFunc{tracking.LibraryWatchState(repo, progStore)}}
	if metaSrc.TMDB != nil {
		tracker.Skeleton = tracking.NewTMDBSkeleton(metaSrc.TMDB)
	}
	ext.trackingHooks(tracker)
	// external accounts (Shikimori lists into marks, local changes and
	// watched episodes pushed back) and the anime map they need
	accts, err := newAccounts(ctx, database, trackStore, tracker, metaSrc.TMDB, repo, progStore, cfg.PublicURL)
	if err != nil {
		return err
	}
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
		Accel: accel, TranscodingDefaults: hwDefaults,
		ScreenTime: screenTime,
		Users:      users, Sessions: sessions, Network: network, Throttle: throttle,
		MediaKeys: auth.NewMediaKeys(mkSecret, sessions),
		Library:   repo, Progress: progStore, Jobs: jobs, HLS: hls, Tracking: tracker,
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
		Accounts:     accts.routes,
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
		Library: repo, Progress: progStore, ImageDir: filepath.Join(cfg.DataDir, "images"), DataDir: cfg.DataDir}
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
	if tracker.Skeleton != nil { // episode lists need TMDB
		wg.Add(1)
		go func() { defer wg.Done(); tracker.Run(ctx) }()
	}
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
	accts.start(ctx, &wg)
	ext.start(ctx, &wg)
	// the apps find this server on the home network (VAULT_DISCOVERY, VAULT_MDNS, VAULT_DISCOVERY_PORT)
	wg.Add(1)
	go func() { defer wg.Done(); runDiscovery(ctx, cfg, serverID) }()

	errc := make(chan error, len(servers))
	for i, s := range servers {
		if i == 0 {
			log.Printf("%s %s listening on %s (hw: %s)", serverName, version.Version, s.Addr, accel.Mode())
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

// --- external accounts: the one place internal/accounts and
// internal/animemap meet the core (adapters over tracking and TMDB) ---

const defaultAnimeMapURL = "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-mini.json"

// accountsWiring is what the server gets from the accounts packages: the
// HTTP routes and the background loops.
type accountsWiring struct {
	routes api.AccountRoutes
	start  func(ctx context.Context, wg *sync.WaitGroup)
}

// newAccounts builds the anime map, the Shikimori sync (pull, outbox push,
// OAuth) and its routes, the MyShows puller (read only, hourly, under the
// Shikimori sync's per-profile lock), and subscribes the sync to the
// tracking store's mark changes and the progress store's watched changes.
func newAccounts(ctx context.Context, database *sql.DB, trackStore *tracking.Store, tracker *tracking.Service, tm *tmdb.Client,
	repo *library.Repo, prog *progress.Store, publicURL string) (*accountsWiring, error) {
	keyHex, err := auth.LoadOrCreateSetting(ctx, database, "accounts_token_key", 32)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("accounts token key: malformed setting")
	}
	log := slog.Default().With("component", "accounts")
	amap := &animemap.Map{DB: database, URL: cmp.Or(os.Getenv("VAULT_ANIME_MAP_URL"), defaultAnimeMapURL),
		Log: slog.Default().With("component", "animemap")}
	if tm != nil {
		amap.Search = tmdbAnimeSearch{tm}
	}
	shiki := shikimoriAccount{shikimori.New(cmp.Or(os.Getenv("VAULT_SHIKIMORI_URL"), shikimori.DefaultBase), nil)}
	store := &accounts.Store{DB: database, Key: key}
	syncer := &accounts.Syncer{Accounts: store, Tracking: accountTracker{trackStore},
		Remote: shikimoriRemote{shiki.c}, Map: amap, Kick: tracker.Kick, Log: log, Writer: shiki,
		OAuth: &accounts.OAuth{Store: store, PublicURL: publicURL,
			EnvClientID: os.Getenv("VAULT_SHIKIMORI_CLIENT_ID"), EnvClientSecret: os.Getenv("VAULT_SHIKIMORI_CLIENT_SECRET")},
		Watched: func(ctx context.Context, uid int64, ref accounts.TitleRef) (map[[2]int]bool, error) {
			w := tracker.WatchedBy()
			t, ok, err := trackStore.TitleByID(ctx, ref.ID)
			if w == nil || err != nil || !ok {
				return nil, err
			}
			return w(ctx, uid, t)
		},
		Episodes: func(ctx context.Context, titleID int64) ([]accounts.Episode, error) {
			eps, err := trackStore.Episodes(ctx, titleID)
			out := make([]accounts.Episode, 0, len(eps))
			for _, e := range eps {
				out = append(out, accounts.Episode{Season: e.Season, Number: e.Number, AirDate: e.AirDate})
			}
			return out, err
		}}
	syncer.Push = syncer.PushOne
	trackStore.OnMarkChanged = syncer.MarkChanged
	prog.OnWatchedChange = func(ctx context.Context, uid, itemID int64) {
		if tid, ok := trackedTitleOfItem(ctx, repo, trackStore, itemID); ok {
			syncer.EpisodesChanged(ctx, uid, tid)
		}
	}
	acctAPI := &accounts.API{Sync: syncer, Account: shiki, Log: log, Localize: i18n.Localize,
		Viewer: func(r *http.Request) (accounts.Viewer, bool) {
			u, ok := auth.UserFrom(r.Context())
			return accounts.Viewer{ID: u.ID, IsKid: u.IsKid, IsAdmin: u.IsAdmin}, ok
		}}
	var mshows *myshows.Puller
	if tm != nil {
		msc := myshows.New(cmp.Or(os.Getenv("VAULT_MYSHOWS_URL"), myshows.DefaultBase), nil)
		mshows = &myshows.Puller{Accounts: store, Tracker: accountTracker{trackStore}, Remote: msc,
			Map: &myshows.Mapper{DB: database, Client: msc, Find: tmdbIMDbFind(tm)}, Kick: tracker.Kick,
			Lock: syncer.UserLock, Log: log.With("provider", myshows.Provider)}
		acctAPI.MyShows = myShowsAccount{p: mshows, c: msc}
	}
	return &accountsWiring{routes: api.AccountRoutes{API: acctAPI.Routes, Browser: acctAPI.BrowserRoutes}, start: func(ctx context.Context, wg *sync.WaitGroup) {
		wg.Add(2)
		go func() { defer wg.Done(); amap.Run(ctx) }()
		go func() { defer wg.Done(); syncer.Run(ctx) }()
		if mshows != nil {
			// without TMDB no show maps (and misses would be cached)
			wg.Add(1)
			go func() { defer wg.Done(); mshows.Run(ctx) }()
		}
	}}, nil
}

// trackedTitleOfItem is the tracked title of a library movie or episode
// (its series), if one is tracked.
func trackedTitleOfItem(ctx context.Context, repo *library.Repo, st *tracking.Store, itemID int64) (int64, bool) {
	it, err := repo.Item(ctx, itemID)
	if err != nil {
		return 0, false
	}
	kind := "movie"
	if it.Kind == library.KindEpisode {
		season, err := repo.Item(ctx, it.ParentID)
		if err != nil {
			return 0, false
		}
		if it, err = repo.Item(ctx, season.ParentID); err != nil || it.Kind != library.KindSeries {
			return 0, false
		}
		kind = "series"
	} else if it.Kind != library.KindMovie {
		return 0, false
	}
	if it.TmdbID == 0 {
		return 0, false
	}
	t, ok, err := st.TitleByTMDB(ctx, kind, it.TmdbID)
	return t.ID, err == nil && ok
}

// accountTracker is accounts.Tracker over the tracking store.
type accountTracker struct{ st *tracking.Store }

func accountTitle(t tracking.Title) accounts.TitleRef {
	r := accounts.TitleRef{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID}
	if t.ShikimoriID != nil {
		r.ShikimoriID = *t.ShikimoriID
	}
	return r
}

func (a accountTracker) EnsureTitle(ctx context.Context, kind string, tmdbID int64, name string) (accounts.TitleRef, error) {
	t, err := a.st.EnsureTitle(ctx, kind, tmdbID, name, "")
	return accountTitle(t), err
}

func (a accountTracker) Title(ctx context.Context, id int64) (accounts.TitleRef, bool, error) {
	t, ok, err := a.st.TitleByID(ctx, id)
	return accountTitle(t), ok, err
}

func (a accountTracker) SetShikimoriID(ctx context.Context, titleID, shikimoriID, anidbID int64) error {
	var an *int64
	if anidbID > 0 {
		an = &anidbID
	}
	return a.st.SetExternalIDs(ctx, titleID, &shikimoriID, nil, an)
}

func (a accountTracker) Marks(ctx context.Context, uid int64) ([]accounts.Mark, error) {
	ms, err := a.st.Marks(ctx, uid)
	out := make([]accounts.Mark, 0, len(ms))
	for _, m := range ms {
		out = append(out, accounts.Mark{UserID: m.UserID, TitleID: m.TitleID, State: string(m.State), Score: m.Score,
			UpdatedAt: m.UpdatedAt, Origin: m.Origin})
	}
	return out, err
}

func (a accountTracker) Mark(ctx context.Context, uid, titleID int64) (accounts.Mark, bool, error) {
	ms, err := a.Marks(ctx, uid)
	for _, m := range ms {
		if m.TitleID == titleID {
			return m, true, err
		}
	}
	return accounts.Mark{}, false, err
}

func (a accountTracker) SetMark(ctx context.Context, m accounts.Mark) error {
	return a.st.SetMark(ctx, tracking.Mark{UserID: m.UserID, TitleID: m.TitleID, State: tracking.MarkState(m.State),
		Score: m.Score, UpdatedAt: m.UpdatedAt, Origin: m.Origin})
}

func (a accountTracker) DeleteMark(ctx context.Context, uid, titleID int64, origin string) error {
	return a.st.DeleteMarkFrom(ctx, uid, titleID, origin)
}

// tmdbIMDbFind looks an IMDb id up on TMDB for the MyShows map: a show
// first, else a movie.
func tmdbIMDbFind(tm *tmdb.Client) myshows.FindFunc {
	return func(ctx context.Context, imdb string) (int64, string, bool, error) {
		for _, kind := range []string{"series", "movie"} {
			id, err := tm.Find(ctx, kind, "imdb_id", imdb)
			if err == nil {
				return int64(id), kind, true, nil
			}
			if !errors.Is(err, tmdb.ErrNotFound) {
				return 0, "", false, err
			}
		}
		return 0, "", false, nil
	}
}

// shikimoriRemote is accounts.Remote over the Shikimori client.
type shikimoriRemote struct{ c *shikimori.Client }

func (r shikimoriRemote) AnimeRates(ctx context.Context, userID int64, access string) ([]accounts.RemoteRate, error) {
	rs, err := r.c.AnimeRates(ctx, userID, access)
	switch {
	case errors.Is(err, shikimori.ErrUnauthorized):
		return nil, accounts.ErrRemoteUnauthorized
	case errors.Is(err, shikimori.ErrForbidden):
		return nil, accounts.ErrRemoteForbidden
	case errors.Is(err, shikimori.ErrNotFound):
		return nil, accounts.ErrRemoteNotFound
	case err != nil:
		return nil, err
	}
	out := make([]accounts.RemoteRate, 0, len(rs))
	for _, x := range rs {
		out = append(out, accounts.RemoteRate{ID: x.ID, Status: x.Status, Score: x.Score, Episodes: x.Episodes, UpdatedAt: x.UpdatedAt,
			Anime: accounts.RemoteAnime{ID: x.Anime.ID, Name: x.Anime.Name, Russian: x.Anime.Russian, Kind: x.Anime.Kind, AiredOn: x.Anime.AiredOn,
				Episodes: x.Anime.Episodes, Status: x.Anime.Status}})
	}
	return out, nil
}

// shikimoriAccount is accounts.Account and accounts.Writer over the
// Shikimori client (the OAuth client of each call from accounts.OAuth).
type shikimoriAccount struct{ c *shikimori.Client }

// accountErr maps the client's errors to the accounts package's.
func accountErr(err error) error {
	switch {
	case errors.Is(err, shikimori.ErrUnauthorized):
		return accounts.ErrRemoteUnauthorized
	case errors.Is(err, shikimori.ErrNotFound):
		return accounts.ErrRemoteNotFound
	}
	return err
}

func (s shikimoriAccount) with(c accounts.OAuthClient) *shikimori.Client {
	cl := *s.c
	cl.ClientID, cl.ClientSecret, cl.RedirectURI = c.ID, c.Secret, c.RedirectURI
	return &cl
}

func (s shikimoriAccount) UserByNick(ctx context.Context, nick string) (accounts.RemoteUser, error) {
	u, err := s.c.UserByNick(ctx, nick)
	return accounts.RemoteUser{ID: u.ID, Nickname: u.Nickname}, accountErr(err)
}

func (s shikimoriAccount) AuthorizeURL(c accounts.OAuthClient, state string) string {
	return s.with(c).AuthorizeURL(state)
}

func (s shikimoriAccount) Exchange(ctx context.Context, c accounts.OAuthClient, code string) (accounts.Tokens, error) {
	t, err := s.with(c).Exchange(ctx, code)
	return t, accountErr(err)
}

func (s shikimoriAccount) Whoami(ctx context.Context, access string) (accounts.RemoteUser, error) {
	u, err := s.c.Whoami(ctx, access)
	return accounts.RemoteUser{ID: u.ID, Nickname: u.Nickname}, accountErr(err)
}

func (s shikimoriAccount) Refresh(ctx context.Context, c accounts.OAuthClient, refresh string) (accounts.Tokens, error) {
	t, err := s.with(c).Refresh(ctx, refresh)
	return t, accountErr(err)
}

func rateInput(w accounts.RateWrite) shikimori.RateInput {
	return shikimori.RateInput{Status: w.Status, Score: w.Score, Episodes: w.Episodes}
}

func (s shikimoriAccount) CreateRate(ctx context.Context, access string, userID, animeID int64, w accounts.RateWrite) (accounts.RateWritten, error) {
	r, err := s.c.CreateRate(ctx, access, userID, animeID, rateInput(w))
	return accounts.RateWritten{ID: r.ID, UpdatedAt: r.UpdatedAt}, accountErr(err)
}

func (s shikimoriAccount) UpdateRate(ctx context.Context, access string, rateID int64, w accounts.RateWrite) (accounts.RateWritten, error) {
	r, err := s.c.UpdateRate(ctx, access, rateID, rateInput(w))
	return accounts.RateWritten{ID: r.ID, UpdatedAt: r.UpdatedAt}, accountErr(err)
}

func (s shikimoriAccount) DeleteRate(ctx context.Context, access string, rateID int64) error {
	return accountErr(s.c.DeleteRate(ctx, access, rateID))
}

func (s shikimoriAccount) AnimeEpisodes(ctx context.Context, animeID int64) (int, error) {
	a, err := s.c.Anime(ctx, animeID)
	return a.Episodes, accountErr(err)
}

func (s shikimoriAccount) RateUpdatedAt(ctx context.Context, access string, rateID int64) (time.Time, error) {
	r, err := s.c.UserRate(ctx, access, rateID)
	return r.UpdatedAt, accountErr(err)
}

// tmdbAnimeSearch is animemap.Searcher over the TMDB client.
type tmdbAnimeSearch struct{ c *tmdb.Client }

const tmdbGenreAnimation = 16

func animeCandidates(rs []tmdb.Result) []animemap.Candidate {
	out := make([]animemap.Candidate, 0, len(rs))
	for _, r := range rs {
		out = append(out, animemap.Candidate{TMDBID: int64(r.ID), Name: r.Title, OriginalName: r.OriginalTitle, Year: r.Year,
			Animation: slices.Contains(r.GenreIDs, tmdbGenreAnimation), OriginalLanguage: r.OriginalLanguage})
	}
	return out
}

func (s tmdbAnimeSearch) SearchTV(ctx context.Context, q string, year int) ([]animemap.Candidate, error) {
	rs, err := s.c.SearchTV(ctx, q, year)
	return animeCandidates(rs), err
}

func (s tmdbAnimeSearch) SearchMovie(ctx context.Context, q string, year int) ([]animemap.Candidate, error) {
	rs, err := s.c.SearchMovie(ctx, q, year)
	return animeCandidates(rs), err
}

// myShowsAccount adapts the MyShows client and puller to the accounts API.
type myShowsAccount struct {
	p *myshows.Puller
	c *myshows.Client
}

func (m myShowsAccount) Check(ctx context.Context, login string) error {
	_, err := m.c.ProfileShows(ctx, login)
	switch {
	case errors.Is(err, myshows.ErrNotFound):
		return accounts.ErrRemoteNotFound
	case errors.Is(err, myshows.ErrPrivate):
		return accounts.ErrRemoteForbidden
	}
	return err
}

func (m myShowsAccount) Pull(ctx context.Context, uid int64) error {
	_, err := m.p.PullOne(ctx, uid)
	return err
}
