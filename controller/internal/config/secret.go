package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	envReferencePattern      = regexp.MustCompile(`^\$\{env:([A-Z][A-Z0-9_]*)\}$`)
	fileReferencePattern     = regexp.MustCompile(`^\$\{file:(/[^{}]*)\}$`)
	dataFileReferencePattern = regexp.MustCompile(`^\$\{file:\$\{data\}/([^{}]+)\}$`)
)

type SecretStatus struct {
	Reference     string    `json:"reference"`
	Configured    bool      `json:"configured"`
	Resolved      bool      `json:"resolved"`
	Source        string    `json:"source,omitempty"`
	Status        string    `json:"status,omitempty"`
	LastRefreshAt time.Time `json:"lastRefreshAt,omitempty,omitzero"`
	Error         string    `json:"error"`
}

type SecretRevision struct {
	Revision uint64    `json:"revision"`
	Success  bool      `json:"success"`
	Error    string    `json:"error"`
	Time     time.Time `json:"time"`
}

type secretEntry struct {
	value    []byte
	expires  time.Time
	revision uint64
	status   SecretStatus
}

type Resolver struct {
	mu          sync.Mutex
	env         map[string]string
	dataDir     string
	cache       map[string]*secretEntry
	subscribers map[string]map[uint64]func(SecretRevision)
	serial      map[string]*sync.Mutex
	nextSub     uint64
	now         func() time.Time
	reader      func(string) ([]byte, SecretStatus, error)
}

func NewResolver(environment []string, dataDir string) *Resolver {
	resolver := &Resolver{
		env: environmentMap(environment), dataDir: dataDir, cache: map[string]*secretEntry{},
		subscribers: map[string]map[uint64]func(SecretRevision){},
		serial:      map[string]*sync.Mutex{}, now: time.Now,
	}
	resolver.reader = resolver.read
	return resolver
}

func (r *Resolver) Resolve(reference string, ttl time.Duration) ([]byte, SecretStatus, error) {
	serial := r.serialFor(reference)
	serial.Lock()
	defer serial.Unlock()

	r.mu.Lock()
	if entry := r.cache[reference]; entry != nil {
		if len(entry.value) > 0 && r.now().Before(entry.expires) {
			value := append([]byte(nil), entry.value...)
			status := entry.status
			r.mu.Unlock()
			return value, status, nil
		}
	}
	entry := r.cache[reference]
	if entry == nil {
		entry = &secretEntry{}
		r.cache[reference] = entry
	}
	r.mu.Unlock()

	value, status, err := r.reader(reference)
	r.mu.Lock()
	if err == nil {
		zero(entry.value)
		entry.value = append([]byte(nil), value...)
		entry.expires = r.now().Add(ttl)
		entry.status = status
	}
	r.mu.Unlock()
	if err != nil {
		return nil, status, err
	}
	return append([]byte(nil), value...), status, nil
}

func (r *Resolver) read(reference string) ([]byte, SecretStatus, error) {
	status := SecretStatus{Reference: reference, Configured: reference != "", Error: ""}
	if match := envReferencePattern.FindStringSubmatch(reference); match != nil {
		value, ok := r.env[match[1]]
		status.Source = "env"
		if !ok || value == "" {
			status.Error = "secret_unavailable"
			return nil, status, fmt.Errorf("secret environment reference %s is unavailable", match[1])
		}
		status.Resolved, status.LastRefreshAt = true, r.now().UTC()
		return []byte(value), status, nil
	}
	match := fileReferencePattern.FindStringSubmatch(reference)
	dataMatch := dataFileReferencePattern.FindStringSubmatch(reference)
	if match == nil && dataMatch == nil {
		status.Error = "secret_reference_invalid"
		return nil, status, errors.New("invalid secret reference")
	}
	status.Source = "file"
	var path string
	if dataMatch != nil {
		relative := dataMatch[1]
		if filepath.Clean(relative) != relative || filepath.IsAbs(relative) {
			status.Error = "secret_path_invalid"
			return nil, status, errors.New("secret data path is not clean and relative")
		}
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if part == ".." {
				status.Error = "secret_path_invalid"
				return nil, status, errors.New("secret data path escapes data directory")
			}
		}
		path = filepath.Join(r.dataDir, relative)
	} else {
		path = match[1]
	}
	if filepath.Clean(path) != path {
		status.Error = "secret_path_invalid"
		return nil, status, errors.New("secret path is not clean")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			status.Error = "secret_file_type"
			return nil, status, errors.New("secret file must be regular and not a symlink")
		}
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	if !info.Mode().IsRegular() {
		status.Error = "secret_file_type"
		return nil, status, errors.New("secret file must be regular and not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		status.Error = "secret_file_mode"
		return nil, status, errors.New("secret file must not grant group or other permissions")
	}
	if info.Size() > 64*1024 {
		status.Error = "secret_file_size"
		return nil, status, errors.New("secret file exceeds 64 KiB")
	}
	value, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	if len(value) > 64*1024 {
		zero(value)
		status.Error = "secret_file_size"
		return nil, status, errors.New("secret file exceeds 64 KiB")
	}
	if bytes.HasSuffix(value, []byte("\r\n")) {
		value = bytes.TrimSuffix(value, []byte("\r\n"))
	} else {
		value = bytes.TrimSuffix(value, []byte("\n"))
	}
	if len(value) == 0 {
		status.Error = "secret_empty"
		return nil, status, errors.New("secret file is empty")
	}
	status.Resolved, status.LastRefreshAt = true, r.now().UTC()
	return value, status, nil
}

func (r *Resolver) Refresh(reference string, ttl time.Duration) (SecretStatus, error) {
	serial := r.serialFor(reference)
	serial.Lock()
	defer serial.Unlock()

	value, status, err := r.reader(reference)
	r.mu.Lock()
	entry := r.cache[reference]
	if entry == nil {
		entry = &secretEntry{}
		r.cache[reference] = entry
	}
	entry.revision++
	revision := entry.revision
	if err == nil {
		zero(entry.value)
		entry.value = append([]byte(nil), value...)
		entry.expires = r.now().Add(ttl)
		entry.status = status
	}
	callbacks := make([]func(SecretRevision), 0, len(r.subscribers[reference]))
	for _, callback := range r.subscribers[reference] {
		callbacks = append(callbacks, callback)
	}
	r.mu.Unlock()
	event := SecretRevision{Revision: revision, Success: err == nil, Time: r.now().UTC()}
	if err != nil {
		event.Error = status.Error
	}
	for _, callback := range callbacks {
		callback(event)
	}
	return status, err
}

func (r *Resolver) serialFor(reference string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	serial := r.serial[reference]
	if serial == nil {
		serial = new(sync.Mutex)
		r.serial[reference] = serial
	}
	return serial
}

func (r *Resolver) Subscribe(reference string, fn func(SecretRevision)) func() {
	r.mu.Lock()
	r.nextSub++
	id := r.nextSub
	if r.subscribers[reference] == nil {
		r.subscribers[reference] = map[uint64]func(SecretRevision){}
	}
	r.subscribers[reference][id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.subscribers[reference], id)
		r.mu.Unlock()
	}
}

func (r *Resolver) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.cache {
		zero(entry.value)
	}
	clear(r.cache)
	clear(r.subscribers)
	clear(r.serial)
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func RedactError(message string, secretValues ...string) error {
	for _, secret := range secretValues {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "<redacted>")
		}
	}
	return errors.New(message)
}
