package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const supported = true

// Windows: HKCU\Software\Microsoft\Windows\CurrentVersion\Run
//
// This is the canonical per-user autostart mechanism. The value name appears
// in Task Manager → Startup apps (and Settings → Apps → Startup), where the
// user can disable it if they change their mind. The value data is the
// command line to run; we quote the exe path so spaces in the path work,
// and append the "tray" subcommand.

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "Clawmeter"
)

func install(bin string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runValue, `"`+bin+`" tray`)
}

func uninstall() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(runValue); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

func isInstalled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}
