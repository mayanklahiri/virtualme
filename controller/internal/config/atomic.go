package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var atomicFault struct {
	sync.RWMutex
	hook func(string) error
}

func AtomicWrite(file string, data []byte) error {
	directory := filepath.Dir(file)
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("config data root is not a real directory")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}
	if info, err := os.Lstat(file); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("config destination must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := runAtomicHook("create"); err != nil {
		return err
	}
	temp, err := os.OpenFile(filepath.Join(directory, "."+filepath.Base(file)+".tmp-"+randomSuffix()),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := runAtomicHook("write"); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := runAtomicHook("sync"); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := runAtomicHook("close"); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := runAtomicHook("chmod"); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := runAtomicHook("rename"); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, file); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := runAtomicHook("dirsync"); err != nil {
		return err
	}
	if err := parent.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func runAtomicHook(step string) error {
	atomicFault.RLock()
	hook := atomicFault.hook
	atomicFault.RUnlock()
	if hook != nil {
		return hook(step)
	}
	return nil
}

func randomSuffix() string {
	file, err := os.CreateTemp("", "vm-config-random-")
	if err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	name := filepath.Base(file.Name())
	_ = file.Close()
	_ = os.Remove(file.Name())
	return name
}
