//go:build windows

package change

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsVaultLockPersistsAndCanBeReacquiredAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.lock")
	first, err := acquireVaultLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireVaultLock(path); err == nil {
		first.release()
		t.Fatal("concurrent lock acquisition succeeded")
	}
	first.release()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock file missing after release: %v", err)
	}
	second, err := acquireVaultLock(path)
	if err != nil {
		t.Fatalf("reacquire released lock: %v", err)
	}
	second.release()
}
