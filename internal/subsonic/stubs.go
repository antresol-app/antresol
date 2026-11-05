package subsonic

// Methods Vault has no data for. Clients (Amperfy, Symfonium) call them
// during sync or on the now-playing screen; an empty answer keeps them
// going where "not supported" would show an error.

func (c *call) getScanStatus() *response {
	_, total, err := c.s.Library.Songs(c.ctx, 1, 0)
	if err != nil {
		return c.internal("getScanStatus", err)
	}
	resp := c.ok()
	resp.ScanStatus = &scanStatus{Count: total}
	return resp
}

func (c *call) getNowPlaying() *response {
	resp := c.ok()
	resp.NowPlaying = &entries{Entry: []child{}}
	return resp
}

func (c *call) getArtistInfo() *response {
	resp := c.ok()
	resp.ArtistInfo = &info{}
	return resp
}

func (c *call) getArtistInfo2() *response {
	resp := c.ok()
	resp.ArtistInfo2 = &info{}
	return resp
}

func (c *call) getAlbumInfo() *response {
	resp := c.ok()
	resp.AlbumInfo = &info{}
	return resp
}

func (c *call) getTopSongs() *response {
	resp := c.ok()
	resp.TopSongs = &songs{Song: []child{}}
	return resp
}

func (c *call) getSimilarSongs() *response {
	resp := c.ok()
	resp.SimilarSongs = &songs{Song: []child{}}
	return resp
}

func (c *call) getSimilarSongs2() *response {
	resp := c.ok()
	resp.SimilarSongs2 = &songs{Song: []child{}}
	return resp
}

// getPlayQueue: no saved queue (savePlayQueue keeps nothing).
func (c *call) getPlayQueue() *response  { return c.ok() }
func (c *call) savePlayQueue() *response { return c.ok() }

func (c *call) getBookmarks() *response {
	resp := c.ok()
	resp.Bookmarks = &bookmarks{Bookmark: []struct{}{}}
	return resp
}

func (c *call) getPodcasts() *response {
	resp := c.ok()
	resp.Podcasts = &podcasts{Channel: []struct{}{}}
	return resp
}

func (c *call) getInternetRadioStations() *response {
	resp := c.ok()
	resp.InternetRadioStations = &radioStations{InternetRadioStation: []struct{}{}}
	return resp
}
