package api

import "sync"

// keyedLimit caps concurrent requests per key (a media key reading
// /api/torrent-stream): a leaked keyed URL cannot hold many stream reads
// (disk use, raised read priorities) open at once. AVPlayer opens a few
// range requests at a time; the cap leaves room for that.
type keyedLimit struct {
	max int
	mu  sync.Mutex
	n   map[string]int
}

// mediaKeyTorrentStreams is the cap per media key on /api/torrent-stream.
const mediaKeyTorrentStreams = 6

// acquire takes a slot for key; false when the key already holds max.
func (l *keyedLimit) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		l.n = map[string]int{}
	}
	if l.n[key] >= l.max {
		return false
	}
	l.n[key]++
	return true
}

func (l *keyedLimit) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[key]--; l.n[key] <= 0 {
		delete(l.n, key)
	}
}
