package good

import (
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

var (
	_ = fmt.Sprint
	_ = uuid.Nil
	_ = norm.NFC
)
