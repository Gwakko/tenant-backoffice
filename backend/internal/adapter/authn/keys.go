package authn

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var kidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// keyring holds the verification keys by kid and the single signing key.
type keyring struct {
	public    map[string]ed25519.PublicKey
	activeKID string
	signing   ed25519.PrivateKey
}

// loadKeyring reads every <kid>.pem of dir. Other files are ignored. The
// directory is read once, at start: rotation is "add the new key, switch
// TB_JWT_ACTIVE_KID, restart, remove the old key after the access TTL".
func loadKeyring(dir, activeKID string) (*keyring, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("authn: read keys directory: %w", err)
	}
	kr := &keyring{public: map[string]ed25519.PublicKey{}, activeKID: activeKID}
	for _, e := range entries {
		kid, ok := strings.CutSuffix(e.Name(), ".pem")
		if !ok || e.IsDir() {
			continue
		}
		if !kidPattern.MatchString(kid) {
			return nil, fmt.Errorf("authn: key file %q has an unusable key id", e.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // directory comes from the process config
		if err != nil {
			return nil, fmt.Errorf("authn: read key %q: %w", kid, err)
		}
		priv, pub, err := parseKey(data)
		if err != nil {
			return nil, fmt.Errorf("authn: key %q: %w", kid, err)
		}
		kr.public[kid] = pub
		if kid == activeKID {
			kr.signing = priv
		}
	}
	if _, ok := kr.public[activeKID]; !ok {
		return nil, fmt.Errorf("authn: active key %q is not in the keys directory", activeKID)
	}
	if kr.signing == nil {
		return nil, fmt.Errorf("authn: active key %q has no private key", activeKID)
	}
	return kr, nil
}

// parseKey accepts a PKCS#8 Ed25519 private key or a PKIX Ed25519 public key.
// The private part is nil for a public key.
func parseKey(data []byte) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, nil, errors.New("not a PEM file")
	}
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, errors.New("invalid PKCS#8 private key")
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, nil, errors.New("not an Ed25519 key")
		}
		return priv, priv.Public().(ed25519.PublicKey), nil
	case "PUBLIC KEY":
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, nil, errors.New("invalid PKIX public key")
		}
		pub, ok := k.(ed25519.PublicKey)
		if !ok {
			return nil, nil, errors.New("not an Ed25519 key")
		}
		return nil, pub, nil
	}
	return nil, nil, fmt.Errorf("unsupported PEM block %q", block.Type)
}
