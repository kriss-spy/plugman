//go:build !windows

package change

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type vaultLock struct{ file *os.File }

func acquireVaultLock(path string) (*vaultLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Vault lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Vault lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another Plugman mutation is active in this Vault: %w", err)
	}
	return &vaultLock{file: file}, nil
}

func (l *vaultLock) release() {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
