package mediaimport

import "context"

// loadJellyfin reads Jellyfin's marks: over its API when a connection is
// kept, else from a snapshot of its database (errNoSourceDB: not mounted).
func loadJellyfin(ctx context.Context, o ProgressOptions) (jfData, error) {
	if r, ok := o.Remotes[SourceJellyfin]; ok {
		c, _, err := jellyfinClient(ctx, r, o.HTTP)
		if err != nil {
			return jfData{}, err
		}
		return readJellyfinAPI(ctx, c)
	}
	d, err := snapshotDB(o.Tmp, o.JellyfinDB)
	if err != nil {
		return jfData{}, err
	}
	defer d.Close()
	return readJellyfin(ctx, d.DB)
}

// loadKavita is loadJellyfin for Kavita.
func loadKavita(ctx context.Context, o ProgressOptions) ([]kvRow, error) {
	if r, ok := o.Remotes[SourceKavita]; ok {
		return readKavitaAPI(ctx, r, o.HTTP)
	}
	d, err := snapshotDB(o.Tmp, o.KavitaDB)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return readKavita(ctx, d.DB)
}

// loadABS is loadJellyfin for Audiobookshelf; others (the positions not
// carried over) only when withOthers.
func loadABS(ctx context.Context, o ProgressOptions, withOthers bool) ([]absRow, map[string]int, error) {
	if r, ok := o.Remotes[SourceABS]; ok {
		return readABSAPI(ctx, r, o.HTTP)
	}
	d, err := snapshotDB(o.Tmp, o.ABSDB)
	if err != nil {
		return nil, nil, err
	}
	defer d.Close()
	list, err := readABS(ctx, d.DB)
	if err != nil || !withOthers {
		return list, nil, err
	}
	others, err := readABSOthers(ctx, d.DB)
	return list, others, err
}
