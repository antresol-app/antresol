-- client_at: the time a client gave for a timed play (the app's offline
-- queue). It is the play's identity on retry: a future time is stored as
-- the server's now in played_at, and a retry of it matches client_at.
ALTER TABLE plays ADD COLUMN client_at TEXT;
CREATE INDEX plays_client ON plays(user_id, item_id, client_at);
