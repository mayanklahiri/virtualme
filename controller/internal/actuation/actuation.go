// Package actuation serializes OS-level input across controller subsystems.
package actuation

import "sync"

var global sync.Mutex

// Lock waits for exclusive access to OS-level actuation.
func Lock() {
	global.Lock()
}

// TryLock acquires exclusive access without waiting.
func TryLock() bool {
	return global.TryLock()
}

// Unlock releases exclusive access to OS-level actuation.
func Unlock() {
	global.Unlock()
}
