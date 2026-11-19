-- A separate history for the apps of third-party protocols (Jellyfin,
-- Subsonic, OPDS). They have no device switch: what binds one to a managed
-- account is its credential. A managed account may have an app password of
-- its own (users.app_password of its row, which no login looks up by name:
-- the app signs in with the person's name and that password). A Jellyfin
-- token got with it remembers the account here, so the token's requests
-- stay the person's and their history goes to the account. Deleting the
-- account (or its merge into the person) ends the tokens got with its
-- password, as a new or removed app password ends the tokens got with the
-- old one. A nullable addition: nothing is rewritten.
ALTER TABLE app_tokens ADD COLUMN history_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX app_tokens_history ON app_tokens(history_id) WHERE history_id IS NOT NULL;
