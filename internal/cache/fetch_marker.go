package cache

import (
	"os"
	"path/filepath"
	"time"
)

// fetchMarkerName names the file that marks a usage fetch in progress. It sits
// beside the usage cache, so every process on the machine sees the same marker.
const fetchMarkerName = "usage.fetching"

// FetchMarkerStale is the age past which a fetch marker is taken to be left by
// a process that died mid-fetch. A live fetch runs under a 30 second context, so
// a marker this old cannot belong to a fetch still in progress.
const FetchMarkerStale = time.Minute

// TakeFetchMarker claims the right to fetch usage for the whole machine. It
// returns ok false while another process holds a marker younger than
// FetchMarkerStale. The caller calls release after writing the fetched data, so
// that readers waiting on the marker find the cache filled rather than fetching
// again.
func TakeFetchMarker() (release func(), ok bool, err error) {
	path, err := Path(fetchMarkerName)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	// Two attempts: the first may find a stale marker, which is removed, and the
	// second claims it.
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue // removed since the failed create; try again
		}
		if time.Since(info.ModTime()) < FetchMarkerStale {
			return nil, false, nil
		}
		os.Remove(path)
	}
	return nil, false, nil
}
