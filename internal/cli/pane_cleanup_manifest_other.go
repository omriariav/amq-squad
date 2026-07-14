//go:build !darwin && !linux

package cli

import "fmt"

func (filesystemPaneCleanupManifestStore) Prepare(string, paneCleanupManifest) (paneCleanupManifestHandle, error) {
	return paneCleanupManifestHandle{}, fmt.Errorf("pane cleanup manifests are unsupported on this platform")
}

func (filesystemPaneCleanupManifestStore) Finalize(paneCleanupManifestHandle, paneCleanupManifest) error {
	return fmt.Errorf("pane cleanup manifests are unsupported on this platform")
}
