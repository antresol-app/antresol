-- «Уведомления на почту» from addons (internal/notify): a profile's choice
-- for each notification topic an addon's manifest declares. A profile with
-- no row for a topic takes the notify_prefs column of the same topic name
-- when there is one, else the topic is off. Queued events of these topics
-- are notify_events rows with topic 'addon:<addon_id>:<topic>'. Removing an
-- addon deletes its rows (and its unsent events).
CREATE TABLE notify_addon_prefs (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addon_id TEXT NOT NULL,
  topic    TEXT NOT NULL,
  enabled  INTEGER NOT NULL,
  PRIMARY KEY (user_id, addon_id, topic)
);
