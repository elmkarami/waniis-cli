package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

func sample(host string) *Credentials {
	return &Credentials{Host: host, Token: "waniis_pat_x", Kind: KindPersonal, Login: "maya"}
}

func TestFileStoreIs0600AndRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "waniis")
	fs := NewFileStore(dir)
	if c, err := fs.Load("h"); c != nil || err != nil {
		t.Fatalf("empty store: %v %v", c, err)
	}
	if err := fs.Save(sample("h")); err != nil {
		t.Fatal(err)
	}
	if err := fs.Save(sample("other")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, "credentials"))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v err = %v", st.Mode().Perm(), err)
		}
		ds, _ := os.Stat(dir)
		if ds.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v", ds.Mode().Perm())
		}
	}
	c, _ := fs.Load("h")
	if c == nil || c.Login != "maya" {
		t.Fatalf("load: %+v", c)
	}
	if err := fs.Delete("h"); err != nil {
		t.Fatal(err)
	}
	if c, _ := fs.Load("h"); c != nil {
		t.Fatal("deleted")
	}
	if c, _ := fs.Load("other"); c == nil {
		t.Fatal("other host kept")
	}
	if err := fs.Delete("never-there"); err != nil {
		t.Fatal(err)
	}
}

func TestKeyringStoreRoundTripsUnderServiceWaniis(t *testing.T) {
	keyring.MockInit()
	ks := KeyringStore{}
	if c, err := ks.Load("h"); c != nil || err != nil {
		t.Fatalf("empty: %v %v", c, err)
	}
	if err := ks.Save(sample("h")); err != nil {
		t.Fatal(err)
	}
	raw, err := keyring.Get(KeychainService, "h")
	if err != nil || KeychainService != "waniis" || raw == "" {
		t.Fatalf("expected an entry under service waniis: %v", err)
	}
	c, _ := ks.Load("h")
	if c == nil || c.Token != "waniis_pat_x" {
		t.Fatalf("%+v", c)
	}
	if err := ks.Delete("h"); err != nil {
		t.Fatal(err)
	}
	if err := ks.Delete("h"); err != nil {
		t.Fatalf("deleting a missing entry is fine: %v", err)
	}
}

type brokenStore struct{}

func (brokenStore) Load(string) (*Credentials, error) { return nil, errors.New("no keychain") }
func (brokenStore) Save(*Credentials) error           { return errors.New("no keychain") }
func (brokenStore) Delete(string) error               { return errors.New("no keychain") }

func TestChainStoreFallsBackToFileWhenNoKeychain(t *testing.T) {
	fs := NewFileStore(t.TempDir())
	var said string
	cs := &ChainStore{Primary: brokenStore{}, Fallback: fs, Notify: func(m string) { said = m }}
	if err := cs.Save(sample("h")); err != nil {
		t.Fatal(err)
	}
	if said == "" {
		t.Fatal("the fallback should be announced")
	}
	if c, _ := cs.Load("h"); c == nil {
		t.Fatal("loaded from the file")
	}
	if err := cs.Delete("h"); err != nil {
		t.Fatalf("logout must work without a keychain: %v", err)
	}
	if c, _ := fs.Load("h"); c != nil {
		t.Fatal("deleted from the file")
	}
}

func TestChainStorePrefersKeychainAndRemovesOldFile(t *testing.T) {
	keyring.MockInit()
	fs := NewFileStore(t.TempDir())
	_ = fs.Save(sample("h"))
	cs := &ChainStore{Primary: KeyringStore{}, Fallback: fs}
	if err := cs.Save(sample("h")); err != nil {
		t.Fatal(err)
	}
	if c, _ := fs.Load("h"); c != nil {
		t.Fatal("the plaintext copy should be dropped once the keychain has it")
	}
}

func TestKindOf(t *testing.T) {
	if KindOf("waniis_cli_abc") != KindGrant || KindOf("waniis_pat_abc") != KindPersonal {
		t.Fatal("kinds")
	}
}
