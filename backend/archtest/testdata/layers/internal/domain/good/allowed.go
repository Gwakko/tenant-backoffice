package good

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

var (
	_ = errors.New
	_ = strings.ToLower
	_ = time.Second
	_ = utf8.RuneLen
	_ apperr.Error
)
