-- An artist's page lists the albums of others with tracks by the artist
-- (compilations): read from the artist's own tracks through this index, not
-- by probing every album's tracks (23 ms -> 0.1 ms on 2000 albums).
CREATE INDEX tracks_artist_nocase ON tracks(artist COLLATE NOCASE);
