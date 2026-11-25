package bad

import (
	"crypto/sha256"
	"log/slog"
	"strings"
)

var (
	_ = sha256.Sum256
	_ = slog.Info
	_ = strings.ToLower
)
