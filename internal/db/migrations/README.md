# Migrations

Each `.sql` file here runs once per database, in a transaction, in order of
its number (the digits before the first `_`). The database records only the
number, in `schema_migrations`.

Numbering:

- One numbering is shared by this folder and by every other set of
  migrations a build adds (packs). `db.Open` refuses to start when two files
  have the same number, in one folder or across folders.
- Core migrations take the next free number above `0072` and stay below
  `10000`. Numbers up to `0072` that are missing here are already used
  elsewhere and are never reused.
- Pack migrations start at `10001`.
- A file that has shipped is never renamed, renumbered or edited: databases
  that ran it would not run it again. Fix a mistake with a new migration.

`TestCoreMigrationNumbersInRange` (in `internal/db`) checks these rules for
this folder.
