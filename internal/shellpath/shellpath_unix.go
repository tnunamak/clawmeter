//go:build !windows

package shellpath

func pathEntryKey(path string) string {
	return path
}

func capture() []string {
	return captureLoginShell()
}
