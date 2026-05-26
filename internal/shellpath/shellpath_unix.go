//go:build !windows

package shellpath

func capture() []string {
	return captureLoginShell()
}
