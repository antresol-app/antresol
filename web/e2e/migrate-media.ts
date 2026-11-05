// Old services for migrate.spec («Переезд на Vault»): Sonarr, Radarr and
// Jellyfin databases with the tables the importers read (the same minimal
// schemas as the Go fixtures in internal/migrate/*fixture_test.go), written
// under <importDir>. Nothing else is there, so the other sources (Kavita,
// Audiobookshelf, ...) are «Не найден».
import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { FAKE_MOVIE, FAKE_SERIES, FAKE_SERIES_2 } from './fakes'
import { ADMIN, LIBRARY_MOVIE } from './vault'

/** The two series Sonarr follows; each has one monitored episode without a file. */
export const MIGRATE_SERIES = [FAKE_SERIES, FAKE_SERIES_2]
/** Radarr's movie without a file. */
export const MIGRATE_MOVIE = FAKE_MOVIE

function db(file: string, schema: string[], fill: (d: DatabaseSync) => void) {
  mkdirSync(path.dirname(file), { recursive: true })
  const d = new DatabaseSync(file)
  try {
    for (const s of schema) d.exec(s)
    fill(d)
  } finally {
    d.close()
  }
}

export function seedMigrate(importDir: string, mediaRoot: string) {
  db(
    path.join(importDir, 'sonarr', 'sonarr.db'),
    [
      `CREATE TABLE "Series" ("Id" INTEGER PRIMARY KEY AUTOINCREMENT, "TvdbId" INTEGER NOT NULL, "TvRageId" INTEGER NOT NULL DEFAULT 0,
        "ImdbId" TEXT, "Title" TEXT NOT NULL, "TitleSlug" TEXT, "CleanTitle" TEXT, "Status" INTEGER NOT NULL DEFAULT 0,
        "Images" TEXT, "Path" TEXT, "Monitored" INTEGER NOT NULL, "Year" INTEGER, "Tags" TEXT,
        "MonitorNewItems" INTEGER NOT NULL DEFAULT 0, "TmdbId" INTEGER DEFAULT 0)`,
      `CREATE TABLE "Episodes" ("Id" INTEGER PRIMARY KEY AUTOINCREMENT, "SeriesId" INTEGER NOT NULL, "SeasonNumber" INTEGER NOT NULL,
        "EpisodeNumber" INTEGER NOT NULL, "Title" TEXT, "Overview" TEXT, "EpisodeFileId" INTEGER DEFAULT 0,
        "AirDate" TEXT, "Monitored" INTEGER NOT NULL DEFAULT 0, "AbsoluteEpisodeNumber" INTEGER, "AirDateUtc" DATETIME)`,
    ],
    (d) => {
      const series = d.prepare('INSERT INTO Series (Id, TvdbId, Title, Images, Path, Monitored, Year, TmdbId) VALUES (?, ?, ?, ?, ?, 1, ?, ?)')
      const ep = d.prepare('INSERT INTO Episodes (SeriesId, SeasonNumber, EpisodeNumber, Monitored, EpisodeFileId) VALUES (?, 1, ?, ?, 0)')
      MIGRATE_SERIES.forEach((s, i) => {
        series.run(i + 1, 70000 + i, s.title, '[]', `/srv/media/media/series/${s.original}`, s.year, s.id)
        for (let e = 1; e <= s.episodes; e++) ep.run(i + 1, e, e === 1 ? 1 : 0) // only episode 1 is wanted
      })
    },
  )
  db(
    path.join(importDir, 'radarr', 'radarr.db'),
    [
      `CREATE TABLE "Movies" ("Id" INTEGER PRIMARY KEY AUTOINCREMENT, "Path" TEXT NOT NULL, "Monitored" INTEGER NOT NULL,
        "QualityProfileId" INTEGER NOT NULL DEFAULT 1, "Added" DATETIME, "Tags" TEXT, "AddOptions" TEXT,
        "MovieFileId" INTEGER DEFAULT 0, "MinimumAvailability" INTEGER NOT NULL DEFAULT 3,
        "MovieMetadataId" INTEGER NOT NULL, "LastSearchTime" DATETIME)`,
      `CREATE TABLE "MovieMetadata" ("Id" INTEGER PRIMARY KEY AUTOINCREMENT, "TmdbId" INTEGER NOT NULL, "ImdbId" TEXT,
        "Images" TEXT NOT NULL DEFAULT '[]', "Title" TEXT NOT NULL, "OriginalTitle" TEXT, "Year" INTEGER)`,
    ],
    (d) => {
      d.prepare('INSERT INTO MovieMetadata (Id, TmdbId, Title, Year) VALUES (100, ?, ?, ?)').run(MIGRATE_MOVIE.id, MIGRATE_MOVIE.title, MIGRATE_MOVIE.year)
      d.prepare('INSERT INTO Movies (Path, Monitored, MovieFileId, MovieMetadataId) VALUES (?, 1, 0, 100)').run(`/srv/media/media/movies/${MIGRATE_MOVIE.original}`)
    },
  )
  // Jellyfin: «Александр» watched the sample movie to the end
  const moviePath = path.join(mediaRoot, 'media', 'movies', `${LIBRARY_MOVIE.title} (${LIBRARY_MOVIE.year})`, `Interstellar.${LIBRARY_MOVIE.year}.1080p.BluRay.mkv`)
  db(
    path.join(importDir, 'jellyfin', 'jellyfin.db'),
    [
      `CREATE TABLE "BaseItems" ("Id" TEXT NOT NULL PRIMARY KEY, "Type" TEXT NOT NULL, "Name" TEXT, "Path" TEXT,
        "RunTimeTicks" INTEGER, "IndexNumber" INTEGER, "ParentIndexNumber" INTEGER, "SeriesId" TEXT, "SeasonId" TEXT,
        "ParentId" TEXT, "SeriesName" TEXT, "ProductionYear" INTEGER, "IsFolder" INTEGER NOT NULL DEFAULT 0)`,
      `CREATE TABLE "BaseItemProviders" ("ItemId" TEXT NOT NULL, "ProviderId" TEXT NOT NULL, "ProviderValue" TEXT NOT NULL,
        PRIMARY KEY ("ItemId", "ProviderId"))`,
      `CREATE TABLE "Users" ("Id" TEXT NOT NULL PRIMARY KEY, "Username" TEXT NOT NULL, "Password" TEXT,
        "MustUpdatePassword" INTEGER NOT NULL DEFAULT 0, "InternalId" INTEGER NOT NULL DEFAULT 0)`,
      `CREATE TABLE "UserData" ("ItemId" TEXT NOT NULL, "UserId" TEXT NOT NULL, "CustomDataKey" TEXT NOT NULL,
        "AudioStreamIndex" INTEGER, "IsFavorite" INTEGER NOT NULL, "LastPlayedDate" TEXT, "Likes" INTEGER,
        "PlayCount" INTEGER NOT NULL DEFAULT 0, "PlaybackPositionTicks" INTEGER NOT NULL, "Played" INTEGER NOT NULL,
        "Rating" REAL, "SubtitleStreamIndex" INTEGER, "RetentionDate" TEXT,
        PRIMARY KEY ("ItemId", "UserId", "CustomDataKey"))`,
    ],
    (d) => {
      d.prepare('INSERT INTO Users (Id, Username) VALUES (?, ?)').run('u-alex', ADMIN.name)
      d.prepare('INSERT INTO BaseItems (Id, Type, Name, Path, RunTimeTicks) VALUES (?, ?, ?, ?, ?)').run(
        'm-interstellar',
        'MediaBrowser.Controller.Entities.Movies.Movie',
        LIBRARY_MOVIE.title,
        moviePath,
        169 * 60 * 10_000_000,
      )
      d.prepare(
        `INSERT INTO UserData (ItemId, UserId, CustomDataKey, IsFavorite, LastPlayedDate, PlaybackPositionTicks, Played)
         VALUES ('m-interstellar', 'u-alex', 'm-interstellar', 0, '2026-10-01 20:00:00.0000000', 0, 1)`,
      ).run()
    },
  )
}
