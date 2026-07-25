package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	envReferencePattern  = regexp.MustCompile(`^\$\{env:([A-Z][A-Z0-9_]*)\}$`)
	fileReferencePattern = regexp.MustCompile(`^\$\{file:(/[^{}]*)\}$`)
)

type SecretStatus struct {
	Reference     string    `json:"reference"`
	Configured    bool      `json:"configured"`
	Resolved      bool      `json:"resolved"`
	Source        string    `json:"source,omitempty"`
	Status        string    `json:"status,omitempty"`
	LastRefreshAt time.Time `json:"lastRefreshAt,omitempty"`
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
	loading  chan struct{}
}

type Resolver struct {
	mu          sync.Mutex
	env         map[string]string
	cache       map[string]*secretEntry
	subscribers map[string]map[uint64]func(SecretRevision)
	serial      map[string]*sync.Mutex
	nextSub     uint64
	now         func() time.Time
}

func NewResolver(environment []string) *Resolver {
	return &Resolver{
		env: environmentMap(environment), cache: map[string]*secretEntry{},
		subscribers: map[string]map[uint64]func(SecretRevision){},
		serial:      map[string]*sync.Mutex{}, now: time.Now,
	}
}

func (r *Resolver) Resolve(reference string, ttl time.Duration) ([]byte, SecretStatus, error) {
	r.mu.Lock()
	if entry := r.cache[reference]; entry != nil {
		if entry.loading != nil {
			wait := entry.loading
			r.mu.Unlock()
			<-wait
			return r.Resolve(reference, ttl)
		}
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
	entry.loading = make(chan struct{})
	wait := entry.loading
	r.mu.Unlock()

	value, status, err := r.read(reference)
	r.mu.Lock()
	entry.loading = nil
	if err == nil {
		zero(entry.value)
		entry.value = append([]byte(nil), value...)
		entry.expires = r.now().Add(ttl)
		entry.status = status
	}
	close(wait)
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
	if match == nil {
		status.Error = "secret_reference_invalid"
		return nil, status, errors.New("invalid secret reference")
	}
	path := match[1]
	status.Source = "file"
	if filepath.Clean(path) != path {
		status.Error = "secret_path_invalid"
		return nil, status, errors.New("secret path is not clean")
	}
	info, err := os.Lstat(path)
	if err != nil {
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
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
	value, err := os.ReadFile(path)
	if err != nil {
		status.Error = "secret_unavailable"
		return nil, status, errors.New("secret file is unavailable")
	}
	value = []byte(strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r"))
	if len(value) == 0 {
		status.Error = "secret_empty"
		return nil, status, errors.New("secret file is empty")
	}
	status.Resolved, status.LastRefreshAt = true, r.now().UTC()
	return value, status, nil
}

func (r *Resolver) Refresh(reference string, ttl time.Duration) (SecretStatus, error) {
	r.mu.Lock()
	serial := r.serial[reference]
	if serial == nil {
		serial = new(sync.Mutex)
		r.serial[reference] = serial
	}
	r.mu.Unlock()
	serial.Lock()
	defer serial.Unlock()

	value, status, err := r.read(reference)
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
