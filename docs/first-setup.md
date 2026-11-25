# First setup

After you create the administrator, a four-step wizard opens. Every step can be skipped: the
same checks stay in the server settings.

![The first-run wizard, Media folders step](images/guide/en/wizard.png)

## Step 1. Media folders

The wizard shows what the server sees under `/media` and finds your films, series, music and
books.

- If a folder has another name, pick it from the list.
- One library can have several folders, on other disks too.
- **Create** makes a missing folder.

The server restarts for a few seconds after you choose.

## Step 2. Write access

The server needs to write to the media folder: subtitles and temporary files go there. If it
cannot, the wizard shows a ready command: copy it and run it on the computer.

## Step 3. Graphics card

The wizard shows whether a graphics card was found, and if not, what to uncomment in
`compose.yml`.

## Step 4. External services

Open services that add to what is already in the library: Wikidata (franchises, Rotten Tomatoes
scores), IMDb ratings, song lyrics (LRCLIB), intros and credits (TheIntroDB, IntroDB). Each row
says what it sends and what it gives; all are turned on here, turn off what you don't need. See
[External services](configuration.html#external-services).

**Done** saves the choice and starts the first scan. Films appear within a few minutes.

## Where the settings are

Click your avatar → **Settings**. There are two parts on the left:

- **My Settings**: interface language, audio and subtitles, notifications, Kindle address, app
  password, your own devices. Each profile has its own.
- **Server**: everything only the administrator sees: profiles, library, watching, TV channels,
  health and background tasks, processing, mail, extensions, the server's address.

![Settings: My Settings and Server on the left](images/guide/en/my-settings.png)

## Family profiles

**Settings → Server → Profiles → New Profile.**

- An adult profile can have a password. A profile without one opens only from the home network.
- A kid profile sees only what suits its age.
- A kid can ask a parent for a film or series the library does not have (**Ask**; the server needs
  a TMDB key). You answer in **Settings → Server → Requests**: approve it and add the title to the
  library by any means; the kid sees it as soon as it is there. **Kids can ask** at the top of that
  page turns requests off.

![Family profiles](images/guide/en/profiles.png)
