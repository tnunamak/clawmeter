package systray

import (
	"github.com/godbus/dbus/v5"
	"github.com/tnunamak/clawmeter/internal/systray/internal/generated/notifier"
)

type leftRightNotifierItem struct {
}

func newLeftRightNotifierItem() notifier.StatusNotifierItemer {
	return &leftRightNotifierItem{}
}

func (i *leftRightNotifierItem) Activate(_, _ int32) *dbus.Error {
	if f := tappedLeft; f == nil {
		return &dbus.ErrMsgUnknownMethod
	}

	tappedLeft()
	return nil
}

func (i *leftRightNotifierItem) ContextMenu(_, _ int32) *dbus.Error {
	if f := tappedRight; f == nil {
		return &dbus.ErrMsgUnknownMethod
	}

	tappedRight()
	return nil
}

func (i *leftRightNotifierItem) SecondaryActivate(_, _ int32) *dbus.Error {
	if f := tappedRight; f == nil {
		return &dbus.ErrMsgUnknownMethod
	}

	tappedRight()
	return nil
}

func (i *leftRightNotifierItem) Scroll(_ int32, _ string) *dbus.Error {
	return &dbus.ErrMsgUnknownMethod
}
