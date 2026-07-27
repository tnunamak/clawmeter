//go:build darwin

package shellpath

import "golang.org/x/sys/unix"

func rootProcessExited(pid int) (bool, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return false, err
	}
	defer unix.Close(kq)

	change := unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	zero := unix.Timespec{}
	if _, err := unix.Kevent(kq, []unix.Kevent_t{change}, nil, &zero); err != nil {
		return false, err
	}

	events := make([]unix.Kevent_t, 1)
	n, err := unix.Kevent(kq, nil, events, &zero)
	return n > 0, err
}
