package balance

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// OperationID identifies a ledger operation.
type OperationID uuid.UUID

// TransferID links the two operations of one transfer.
type TransferID uuid.UUID

// MaxCommentRunes is the longest allowed comment.
const MaxCommentRunes = 200

// Comment is a normalized operation comment (possibly empty).
type Comment struct{ v string }

// ParseComment validates and normalizes a raw comment: UTF-8, inner whitespace
// collapsed to one space and trimmed, no control characters, 0..200 runes.
func ParseComment(raw string) (Comment, error) {
	if !utf8.ValidString(raw) {
		return Comment{}, invalidComment("comment is not valid UTF-8")
	}
	s := strings.Join(strings.Fields(raw), " ")
	for _, r := range s {
		if unicode.IsControl(r) {
			return Comment{}, invalidComment("comment contains control characters")
		}
	}
	if utf8.RuneCountInString(s) > MaxCommentRunes {
		return Comment{}, invalidComment("comment must be at most 200 characters")
	}
	return Comment{v: s}, nil
}

func invalidComment(msg string) error {
	return apperr.New(apperr.ValidationFailed, msg).WithField("comment", string(apperr.ValidationFailed))
}

// String returns the normalized comment.
func (c Comment) String() string { return c.v }

// Operation is one immutable ledger record. Corrections are made by a
// counter-operation, never by editing a record.
type Operation struct {
	ID                 OperationID
	NodeID             node.ID
	Kind               Kind
	Amount             money.Amount
	BalanceAfter       money.Amount
	TransferID         *TransferID
	CounterpartyNodeID *node.ID
	Comment            Comment
	ActorID            user.ActorID
	CreatedAt          time.Time
}

// Replay folds a node's operations (in any order) into the balance they imply:
// the sum of increasing kinds minus the sum of decreasing ones (INV-L1). It
// fails if a partial sum leaves the int64 range or the total is negative.
func Replay(ops []Operation) (money.Amount, error) {
	var inc, dec money.Amount
	var err error
	for _, op := range ops {
		if !op.Kind.Valid() {
			return 0, apperr.New(apperr.ValidationFailed, "unknown operation kind")
		}
		if op.Kind.Increases() {
			inc, err = inc.Add(op.Amount)
		} else {
			dec, err = dec.Add(op.Amount)
		}
		if err != nil {
			return 0, err
		}
	}
	return inc.Sub(dec)
}
