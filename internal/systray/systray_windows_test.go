//go:build windows

package systray

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/ioutil"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const iconFilePath = "testdata/iconwin.ico"

func TestBaseWindowsTray(t *testing.T) {
	systrayReady = func() {}
	systrayExit = func() {}
	systrayExitCalled.Store(false)

	runtime.LockOSThread()

	if err := wt.initInstance(); err != nil {
		t.Fatalf("initInstance failed: %s", err)
	}

	if err := wt.createMenu(); err != nil {
		t.Fatalf("createMenu failed: %s", err)
	}

	defer func() {
		pDestroyWindow.Call(uintptr(wt.window))
		wt.wcex.unregister()
	}()

	if err := wt.setIcon(iconFilePath); err != nil {
		t.Errorf("SetIcon failed: %s", err)
	}

	if err := wt.setTooltip("Cyrillic tooltip тест:)"); err != nil {
		t.Errorf("SetIcon failed: %s", err)
	}

	var id atomic.Uint32
	err := wt.addOrUpdateMenuItem(id.Add(1), 0, "Simple enabled", false, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(id.Add(1), 0, "Simple disabled", true, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addSeparatorMenuItem(id.Add(1), 0)
	if err != nil {
		t.Errorf("addSeparatorMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(id.Add(1), 0, "Simple checked enabled", false, true)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}
	err = wt.addOrUpdateMenuItem(id.Add(1), 0, "Simple checked disabled", true, true)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}

	err = wt.hideMenuItem(1, 0)
	if err != nil {
		t.Errorf("hideMenuItem failed: %s", err)
	}

	err = wt.hideMenuItem(100, 0)
	if err == nil {
		t.Error("hideMenuItem failed: must return error on invalid item id")
	}

	err = wt.addOrUpdateMenuItem(2, 0, "Simple disabled update", true, false)
	if err != nil {
		t.Errorf("mergeMenuItem failed: %s", err)
	}

	time.AfterFunc(1*time.Second, quit)

	m := struct {
		WindowHandle windows.Handle
		Message      uint32
		Wparam       uintptr
		Lparam       uintptr
		Time         uint32
		Pt           point
	}{}
	for {
		ret, _, err := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		res := int32(ret)
		if res == -1 {
			t.Errorf("win32 GetMessage failed: %v", err)
			return
		} else if res == 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func TestWindowsRun(t *testing.T) {
	onReady := func() {
		b, err := ioutil.ReadFile(iconFilePath)
		if err != nil {
			t.Fatalf("Can't load icon file: %v", err)
		}
		SetIcon(b)
		SetTitle("Test title с кириллицей")

		bSomeBtn := AddMenuItem("Йа кнопко", "")
		bSomeBtn.Check()
		AddSeparator()
		bQuit := AddMenuItem("Quit", "Quit the whole app")
		go func() {
			<-bQuit.ClickedCh
			t.Log("Quit reqested")
			Quit()
		}()
		time.AfterFunc(1*time.Second, Quit)
	}

	onExit := func() {
		t.Log("Exit success")
	}

	Run(onReady, onExit)
}

var pGetGuiResources = windows.NewLazySystemDLL("User32.dll").NewProc("GetGuiResources")

func guiResources(t *testing.T, flag uintptr) uintptr {
	t.Helper()
	proc, _ := windows.GetCurrentProcess()
	n, _, _ := pGetGuiResources.Call(uintptr(proc), flag)
	return n
}

func testPNG(t *testing.T, i int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(i), G: uint8(i >> 2), B: uint8(x * y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestIconSwapDoesNotLeakGDIOrUserObjects sets 1,000 varied icons through
// pngToHIcon and swapIcon and checks the process handle counts stay flat.
func TestIconSwapDoesNotLeakGDIOrUserObjects(t *testing.T) {
	const GR_GDIOBJECTS, GR_USEROBJECTS = 0, 1
	tray := &winTray{nid: &notifyIconData{}}
	set := func(i int) {
		h, err := pngToHIcon(testPNG(t, i))
		if err != nil {
			t.Fatalf("pngToHIcon: %v", err)
		}
		tray.muNID.Lock()
		defer tray.muNID.Unlock()
		if err := tray.swapIcon(h, true, func() error { return nil }); err != nil {
			t.Fatalf("swapIcon: %v", err)
		}
	}
	for i := 0; i < 50; i++ { // warm up
		set(i)
	}
	gdi0, user0 := guiResources(t, GR_GDIOBJECTS), guiResources(t, GR_USEROBJECTS)
	for i := 0; i < 1000; i++ {
		set(i)
	}
	gdi1, user1 := guiResources(t, GR_GDIOBJECTS), guiResources(t, GR_USEROBJECTS)
	const slack = 20
	if gdi1 > gdi0+slack {
		t.Errorf("GDI objects grew from %d to %d", gdi0, gdi1)
	}
	if user1 > user0+slack {
		t.Errorf("USER objects grew from %d to %d", user0, user1)
	}
}

func TestSwapIconKeepsCurrentIconOnFailure(t *testing.T) {
	tray := &winTray{nid: &notifyIconData{}}
	h, err := pngToHIcon(testPNG(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	tray.muNID.Lock()
	defer tray.muNID.Unlock()
	if err := tray.swapIcon(h, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	h2, _ := pngToHIcon(testPNG(t, 2))
	if err := tray.swapIcon(h2, true, func() error { return errors.New("boom") }); err == nil {
		t.Fatal("want error")
	}
	if tray.nid.Icon != h || tray.ownedIcon != h {
		t.Fatalf("current icon changed after failed swap: nid=%v owned=%v want %v", tray.nid.Icon, tray.ownedIcon, h)
	}
}
