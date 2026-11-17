// Old media servers for migrate.spec («Переезд»): a Jellyfin database with
// the tables the importer reads (the same minimal schema as the Go fixtures
// in internal/migrate/*fixture_test.go), written under <importDir>. Nothing
// else is there, so the other sources (Kavita, Audiobookshelf, ...) are
// «Не найден».
import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { ADMIN, LIBRARY_MOVIE } from './vault'

/** A database at `file`: its schema, then `fill`. */
export function db(file: string, schema: string[], fill: (d: DatabaseSync) => void) {
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
