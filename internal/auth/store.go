package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// KeychainService is the service name entries are stored under in the OS keychain.
const KeychainService = "waniis"

// KeyringStore keeps credentials in the OS keychain (macOS Keychain, Windows Credential Manager,
// the Secret Service on Linux), one entry per host under the service "waniis".
type KeyringStore struct{}

// Load reads the host's entry.
func (KeyringStore) Load(host string) (*Credentials, error) {
	raw, err := keyring.Get(KeychainService, host)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("the keychain entry for %s is unreadable: %w", host, err)
	}
	return &c, nil
}

// Save writes the host's entry.
func (KeyringStore) Save(c *Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return keyring.Set(KeychainService, c.Host, string(b))
}

// Delete removes the host's entry; a missing one is not an error.
func (KeyringStore) Delete(host string) error {
	if err := keyring.Delete(KeychainService, host); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// FileStore keeps credentials in a JSON file readable only by the user (mode 0600), a map by host.
type FileStore struct {
	Path string
	mu   sync.Mutex
}

// NewFileStore builds a FileStore at dir/credentials.
func NewFileStore(dir string) *FileStore { return &FileStore{Path: filepath.Join(dir, "credentials")} }

func (f *FileStore) read() (map[string]*Credentials, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]*Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]*Credentials{}
	if len(b) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("the credentials file %s is unreadable: %w", f.Path, err)
	}
	return m, nil
}

func (f *FileStore) write(m map[string]*Credentials) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

// Load reads the host's credentials.
func (f *FileStore) Load(host string) (*Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return nil, err
	}
	return m[host], nil
}

// Save writes the host's credentials.
func (f *FileStore) Save(c *Credentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	m[c.Host] = c
	return f.write(m)
}

// Delete removes the host's credentials.
func (f *FileStore) Delete(host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := m[host]; !ok {
		return nil
	}
	delete(m, host)
	return f.write(m)
}

// ChainStore prefers the keychain and falls back to the file where there is no keychain (a
// headless Linux box, a container). Save reports which one took the credentials.
type ChainStore struct {
	Primary  Store
	Fallback Store
	// Notify is told when the fallback is used; may be nil.
	Notify func(msg string)
}

// Load looks in the keychain, then in the file.
func (c *ChainStore) Load(host string) (*Credentials, error) {
	if cred, err := c.Primary.Load(host); err == nil && cred != nil {
		return cred, nil
	}
	return c.Fallback.Load(host)
}

// Save tries the keychain and falls back to the file.
func (c *ChainStore) Save(cred *Credentials) error {
	if err := c.Primary.Save(cred); err == nil {
		// Drop any older plaintext copy now that the keychain holds it.
		_ = c.Fallback.Delete(cred.Host)
		return nil
	}
	if c.Notify != nil {
		c.Notify("No OS keychain is available; saved the credentials in a file only you can read.")
	}
	return c.Fallback.Save(cred)
}

// Delete removes the credentials from both.
func (c *ChainStore) Delete(host string) error {
	e1 := c.Primary.Delete(host)
	e2 := c.Fallback.Delete(host)
	if e2 != nil {
		return e2
	}
	_ = e1 // a missing keychain on this machine isn't a failure to log out
	return nil
}

// MemoryStore is an in-memory Store for tests.
type MemoryStore struct {
	mu sync.Mutex
	M  map[string]*Credentials
}

// Load reads the host's credentials.
func (m *MemoryStore) Load(host string) (*Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.M[host], nil
}

// Save writes the host's credentials.
func (m *MemoryStore) Save(c *Credentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.M == nil {
		m.M = map[string]*Credentials{}
	}
	cp := *c
	m.M[c.Host] = &cp
	return nil
}

// Delete removes the host's credentials.
func (m *MemoryStore) Delete(host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.M, host)
	return nil
}
