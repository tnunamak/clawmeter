package cache

import (
	"os"
	"path/filepath"
)

// fetchMarkerName names the lock file that serializes usage fetches. It sits
// beside the usage cache, so every process on the machine locks the same file.
const fetchMarkerName = "usage.fetching"

// TakeFetchMarker claims the right to fetch usage for the whole machine. It
// returns ok false while another process holds the claim. The claim is an OS
// file lock, which the kernel releases when its holder exits, so a crashed
// holder frees it at once. No age is compared: a live holder keeps its claim
// however long its fetch runs. The caller calls release after writing the
// fetched data, so readers waiting on the marker find the cache filled. release
// unlocks the file and leaves it in place. Removing a lock file would let a
// second process lock a new file at the same path while the first still holds
// the old one.
func TakeFetchMarker() (release func(), ok bool, err error) {
	path, err := Path(fetchMarkerName)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	locked, err := tryFileLock(f)
	if err != nil || !locked {
		_ = f.Close()
		return nil, false, err
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, true, nil
}
