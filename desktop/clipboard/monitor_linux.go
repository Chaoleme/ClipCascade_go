//go:build linux

package clipboard

func getPlatformFilePaths() ([]string, error) {
	// Not implemented on Linux pure Go yet. Return gracefully.
	return nil, nil
}

func setPlatformFilePaths(paths []string) error {
	return nil
}

// startPlatformFileWatcher starts a low-frequency polling loop for Linux files.
func (m *Manager) startPlatformFileWatcher() {
	// N/A for Linux pure Go in this iteration.
}
