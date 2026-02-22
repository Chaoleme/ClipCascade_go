//go:build darwin

package clipboard

import (
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// getPlatformFilePaths returns a list of file paths currently in the clipboard using AppleScript.
func getPlatformFilePaths() ([]string, error) {
	cmd := exec.Command("osascript", "-e", "return POSIX path of (the clipboard as «class furl»)")
	out, err := cmd.Output()
	if err != nil {
		// Normal when no file is copied
		return nil, nil
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	// AppleScript returns comma-separated if multiple files are selected
	paths := strings.Split(raw, ", ")
	var validPaths []string
	for _, p := range paths {
		if p != "" {
			validPaths = append(validPaths, p)
		}
	}

	return validPaths, nil
}

func setPlatformFilePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	slog.Info("clipboard: setting file stub to clipboard via osascript...")
	// We'll set the first path for simplicity to prove the lazy drop works conceptually.
	script := "set the clipboard to POSIX file \"" + paths[0] + "\""
	cmd := exec.Command("osascript", "-e", script)
	err := cmd.Run()
	if err != nil {
		slog.Warn("clipboard: failed to set file path", "error", err)
	}
	return err
}

// startPlatformFileWatcher starts a low-frequency polling loop for macOS files.
func (m *Manager) startPlatformFileWatcher() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			paths, err := getPlatformFilePaths()
			if err != nil || len(paths) == 0 {
				continue
			}

			payload := strings.Join(paths, "\n")
			m.handleChange(payload, "file_stub")
		}
	}()
}
