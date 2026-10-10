// Package identity manages this device's permanent Ed25519 keypair.
// The device ID is derived from the public key, so peers recognise each other
// by key rather than by address or server-assigned name.
package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// utf8BOM is tolerated at the start of JSON files, which Windows editors
// and PowerShell often add when someone edits them by hand.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

const fileName = "identity.json"

type Identity struct {
	Name       string             `json:"name"`
	PublicKey  ed25519.PublicKey  `json:"publicKey"`
	PrivateKey ed25519.PrivateKey `json:"privateKey"`
}

// ID returns the stable device ID for this identity.
func (i *Identity) ID() string { return DeviceID(i.PublicKey) }

// DeviceID derives a short, human-readable ID from a public key,
// e.g. "K7QX-M2PA-9TRB-WDNE".
func DeviceID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10])
	return enc[0:4] + "-" + enc[4:8] + "-" + enc[8:12] + "-" + enc[12:16]
}

// LoadOrCreate reads the identity from dir, creating a new one if none exists.
func LoadOrCreate(dir string) (*Identity, error) {
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if err == nil {
		var id Identity
		if err := json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &id); err != nil {
			return nil, fmt.Errorf("parse identity: %w", err)
		}
		if len(id.PrivateKey) != ed25519.PrivateKeySize {
			return nil, errors.New("identity file has an invalid private key")
		}
		return &id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := &Identity{Name: defaultName(), PublicKey: pub, PrivateKey: priv}
	if err := id.Save(dir); err != nil {
		return nil, err
	}
	return id, nil
}

// Save writes the identity to dir with owner-only permissions.
func (i *Identity) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fileName), data, 0o600)
}

func defaultName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "TropicalPeer device"
	}
	return host
}
