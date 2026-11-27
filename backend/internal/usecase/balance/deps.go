// Package balance holds the use cases of balances and the ledger (UC-B1..B4):
// credit, debit, transfer and balance read. Every use case checks the actor's
// rights on every call; a node outside the actor's subtrees is not_found,
// indistinguishable from a missing one.
//
// Money rules enforced here: an amount is a positive int64 in minor units; a
// debit is checked against the locked balance before anything is written
// (insufficient_funds is raised by the use case, never by the database CHECK);
// balances are locked in ascending node id order; the idempotency key and the
// operation are written in the same transaction.
package balance

import (
	"bytes"
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/money"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// KeyTTL is how long a stored idempotency key stays live.
const KeyTTL = 24 * time.Hour

// Limits of an idempotency key.
const (
	maxKeyLen   = 128
	requestHash = 32
)

// Deps are the ports every balance use case works through.
type Deps struct {
	Runner   port.TxRunner
	Checker  authz.Checker
	Nodes    port.NodeRepo
	Balances port.BalanceRepo
	Ops      port.OperationRepo
	Keys     port.IdempotencyRepo
	Limits   port.LimitSource
	Audit    port.AuditWriter
	Clock    port.Clock
	IDs      port.IDGen
}

var (
	readTx  = port.TxOptions{ReadOnly: true}
	writeTx = port.TxOptions{}
)

func (d Deps) require(ctx context.Context, tx port.Tx, actor user.ActorID, id dnode.ID, perm role.Permission) error {
	_, err := authz.Require(ctx, d.Checker, tx, actor, id, perm)
	return err
}

func validation(field, detail, msg string) error {
	return apperr.New(apperr.ValidationFailed, msg).WithField(field, detail)
}

func requireID(field string, id dnode.ID) error {
	if id.IsZero() {
		return validation(field, "required", field+" is required")
	}
	return nil
}

// request is the validated common part of credit, debit and transfer.
type request struct {
	amount  money.Amount
	comment dbalance.Comment
}

func validateCommon(rawAmount, rawComment, key string, hash []byte) (request, error) {
	amount, err := money.ParseAmount(rawAmount)
	if err != nil {
		return request{}, err
	}
	if err := amount.Positive(); err != nil {
		return request{}, err
	}
	comment, err := dbalance.ParseComment(rawComment)
	if err != nil {
		return request{}, err
	}
	if err := validateKey(key); err != nil {
		return request{}, err
	}
	if len(hash) != requestHash {
		return request{}, validation("request_hash", "invalid", "request hash must be 32 bytes")
	}
	return request{amount: amount, comment: comment}, nil
}

func validateKey(key string) error {
	if key == "" {
		return apperr.New(apperr.IdempotencyKeyRequired, "Idempotency-Key is required")
	}
	if len(key) > maxKeyLen {
		return apperr.New(apperr.IdempotencyKeyInvalid, "Idempotency-Key is too long")
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == ':', r == '.':
		default:
			return apperr.New(apperr.IdempotencyKeyInvalid, "Idempotency-Key has a character outside [A-Za-z0-9_:.-]")
		}
	}
	return nil
}

// claim stores the idempotency key in the operation's transaction. When the key
// is already live it returns the stored record with claimed false; a record of
// another request is apperr.IdempotencyKeyReused.
func (d Deps) claim(ctx context.Context, tx port.Tx, rec port.IdempotencyRecord) (claimed bool, existing port.IdempotencyRecord, err error) {
	now := d.Clock.Now()
	rec.CreatedAt, rec.ExpiresAt = now, now.Add(KeyTTL)
	claimed, existing, err = d.Keys.Claim(ctx, tx, rec, now)
	if err != nil || claimed {
		return claimed, existing, err
	}
	if !bytes.Equal(existing.RequestHash, rec.RequestHash) {
		return false, existing, reused()
	}
	return false, existing, nil
}

func reused() error {
	return apperr.New(apperr.IdempotencyKeyReused, "the Idempotency-Key was used for another request")
}

// checkNode verifies that operations are allowed on the node: effectively
// active, enabled by the effective settings, and the amount within the limit.
func (d Deps) checkNode(ctx context.Context, tx port.Tx, id dnode.ID, amount money.Amount) error {
	n, err := d.Nodes.Get(ctx, tx, id)
	if err != nil {
		return err
	}
	lim, err := d.Limits.EffectiveLimits(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := dbalance.CheckOperable(n.EffectiveActive(), lim.OperationsEnabled); err != nil {
		return err
	}
	return dbalance.CheckLimit(amount, lim.MaxOperationAmount)
}

func (d Deps) newOperationID() dbalance.OperationID { return dbalance.OperationID(d.IDs.New()) }

func (d Deps) newTransferID() dbalance.TransferID { return dbalance.TransferID(d.IDs.New()) }

func opUUID(id dbalance.OperationID) uuid.UUID { return uuid.UUID(id) }
