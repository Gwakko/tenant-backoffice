package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// Upper bounds for the parameters read out of a stored hash, so that a
// corrupted value cannot make the server allocate gigabytes.
const (
	maxMemoryKiB = 1 << 20
	maxTime      = 20
	maxKeyLen    = 1024
)

var b64 = base64.RawStdEncoding

// Hasher implements user.Hasher with argon2id and PHC-format strings:
// $argon2id$v=19$m=...,t=...,p=...$salt$hash.
type Hasher struct{ cfg Config }

var _ user.Hasher = (*Hasher)(nil)

// NewHasher validates the argon2id settings of cfg.
func NewHasher(cfg Config) (*Hasher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Hasher{cfg: cfg}, nil
}

// Hash implements user.Hasher.
func (h *Hasher) Hash(plain string) (user.PasswordHash, error) {
	salt := make([]byte, h.cfg.Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("authn: read random salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, h.cfg.Argon2Time, h.cfg.Argon2MemoryKiB, h.cfg.Argon2Threads, h.cfg.Argon2KeyLen)
	return user.PasswordHash(fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.cfg.Argon2MemoryKiB, h.cfg.Argon2Time, h.cfg.Argon2Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key))), nil
}

// Verify implements user.Hasher. The parameters come from the hash itself and
// the digests are compared in constant time.
func (h *Hasher) Verify(hash user.PasswordHash, plain string) (bool, error) {
	p, salt, want, err := parsePHC(string(hash))
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plain), salt, p.time, p.memory, p.threads, uint32(len(want))) //nolint:gosec // length is bounded by maxKeyLen in parsePHC
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type phcParams struct {
	memory, time uint32
	threads      uint8
}

var errMalformedHash = errors.New("authn: malformed password hash")

func parsePHC(s string) (p phcParams, salt, key []byte, err error) {
	parts := strings.Split(s, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errMalformedHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return p, nil, nil, errMalformedHash
	}
	var threads uint32
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return p, nil, nil, errMalformedHash
	}
	for i, f := range fields {
		prefix := [...]string{"m=", "t=", "p="}[i]
		v, ok := strings.CutPrefix(f, prefix)
		if !ok {
			return p, nil, nil, errMalformedHash
		}
		n, perr := strconv.ParseUint(v, 10, 32)
		if perr != nil {
			return p, nil, nil, errMalformedHash
		}
		switch i {
		case 0:
			p.memory = uint32(n)
		case 1:
			p.time = uint32(n)
		case 2:
			threads = uint32(n)
		}
	}
	if p.memory < 8 || p.memory > maxMemoryKiB || p.time < 1 || p.time > maxTime || threads < 1 || threads > 255 {
		return p, nil, nil, errMalformedHash
	}
	p.threads = uint8(threads)
	if salt, err = b64.DecodeString(parts[4]); err != nil || len(salt) < 8 {
		return p, nil, nil, errMalformedHash
	}
	if key, err = b64.DecodeString(parts[5]); err != nil || len(key) < 16 || len(key) > maxKeyLen {
		return p, nil, nil, errMalformedHash
	}
	return p, salt, key, nil
}
