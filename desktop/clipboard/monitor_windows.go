//go:build windows

package clipboard

import (
	"log/slog"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procDragQueryFileW   = shell32.NewProc("DragQueryFileW")

	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

const CF_HDROP = 15

// getPlatformFilePaths queries the Windows CF_HDROP clipboard purely via Go syscalls.
func getPlatformFilePaths() ([]string, error) {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return nil, nil
	}
	defer procCloseClipboard.Call()

	hDrop, _, _ := procGetClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, nil // No files in clipboard
	}

	pDrop, _, _ := procGlobalLock.Call(hDrop)
	if pDrop == 0 {
		return nil, nil
	}
	defer procGlobalUnlock.Call(hDrop)

	count, _, _ := procDragQueryFileW.Call(pDrop, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, nil
	}

	var paths []string
	for i := uintptr(0); i < count; i++ {
		size, _, _ := procDragQueryFileW.Call(pDrop, i, 0, 0)
		if size == 0 {
			continue
		}

		buf := make([]uint16, size+1)
		procDragQueryFileW.Call(pDrop, i, uintptr(unsafe.Pointer(&buf[0])), size+1)

		path := windows.UTF16ToString(buf)
		if path != "" {
			paths = append(paths, path)
		}
	}

	return paths, nil
}

func setPlatformFilePaths(paths []string) error {
	// Lazy deferred rendering is complex to implement purely in Go syscalls in 1 hour.
	// We'll leave it as a no-op stub for now. The focus is to broadcast file_stub successfully.
	slog.Info("clipboard: File stub received, lazy pasting via native API will happen here later")
	return nil
}

// startPlatformFileWatcher starts a low-frequency polling loop for CF_HDROP.
func (m *Manager) startPlatformFileWatcher() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			paths, err := getPlatformFilePaths()
			if err != nil || len(paths) == 0 {
				continue
			}
			
			// We format the payload as a file_stub and trigger the change detector.
			// Format: "C:\path\to\file1.txt\nC:\path\to\file2.jpg"
			payload := strings.Join(paths, "\n")
			m.handleChange(payload, "file_stub")
		}
	}()
}
