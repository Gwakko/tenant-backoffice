package balance

import (
	"bytes"
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// TransferInput is the request of UC-B3.
type TransferInput struct {
	Actor          user.ActorID
	RequestID      string
	FromNodeID     dnode.ID
	ToNodeID       dnode.ID
	Amount         string
	Comment        string
	IdempotencyKey string
	RequestHash    []byte
}

// TransferOutput holds both legs of the transfer.
type TransferOutput struct {
	TransferID dbalance.TransferID
	Out        dbalance.Operation
	In         dbalance.Operation
	Replayed   bool
}

// Transfer is UC-B3.
type Transfer struct{ d Deps }

// NewTransfer builds the use case.
func NewTransfer(d Deps) *Transfer { return &Transfer{d: d} }

// Execute moves the amount from one node to another. The actor needs
// balance.transfer on both nodes; either node outside the actor's subtrees is
// not_found. Both balances are locked in ascending node id order, whatever the
// direction of the transfer, so opposite transfers cannot deadlock.
func (uc *Transfer) Execute(ctx context.Context, in TransferInput) (TransferOutput, error) {
	d := uc.d
	if err := requireID("from_node_id", in.FromNodeID); err != nil {
		return TransferOutput{}, err
	}
	if err := requireID("to_node_id", in.ToNodeID); err != nil {
		return TransferOutput{}, err
	}
	req, err := validateCommon(in.Amount, in.Comment, in.IdempotencyKey, in.RequestHash)
	if err != nil {
		return TransferOutput{}, err
	}
	if in.FromNodeID == in.ToNodeID {
		return TransferOutput{}, apperr.New(apperr.SameNode, "source and destination must differ").
			WithField("to_node_id", string(apperr.SameNode))
	}
	var out TransferOutput
	err = d.Runner.InTx(ctx, in.Actor, writeTx, func(ctx context.Context, tx port.Tx) error {
		for _, id := range []dnode.ID{in.FromNodeID, in.ToNodeID} {
			if err := d.require(ctx, tx, in.Actor, id, role.BalanceTransfer); err != nil {
				return err
			}
		}
		tid := d.newTransferID()
		claimed, existing, err := d.claim(ctx, tx, port.IdempotencyRecord{
			UserID: in.Actor, Key: in.IdempotencyKey, RequestHash: in.RequestHash, TransferID: &tid,
		})
		if err != nil {
			return err
		}
		if !claimed {
			if existing.TransferID == nil {
				return reused()
			}
			o, i, err := d.Ops.GetTransfer(ctx, tx, *existing.TransferID)
			if err != nil {
				return err
			}
			if o.NodeID != in.FromNodeID || i.NodeID != in.ToNodeID {
				return reused()
			}
			out = TransferOutput{TransferID: *existing.TransferID, Out: o, In: i, Replayed: true}
			return nil
		}
		for _, id := range []dnode.ID{in.FromNodeID, in.ToNodeID} {
			if err := d.checkNode(ctx, tx, id, req.amount); err != nil {
				return err
			}
		}
		balances, err := lockAscending(ctx, d, tx, d.Clock.Now(), in.FromNodeID, in.ToNodeID)
		if err != nil {
			return err
		}
		// Stamped under both row locks (see operate).
		// The database keeps microseconds; the answer must equal what a replay reads back.
		now := d.Clock.Now().Truncate(time.Microsecond)
		from, to, err := dbalance.Transfer(balances[in.FromNodeID], balances[in.ToNodeID], req.amount, now)
		if err != nil {
			return err
		}
		from2, to2 := in.FromNodeID, in.ToNodeID
		opOut := dbalance.Operation{
			ID: d.newOperationID(), NodeID: from2, Kind: dbalance.TransferOut, Amount: req.amount,
			BalanceAfter: from.Amount, TransferID: &tid, CounterpartyNodeID: &to2,
			Comment: req.comment, ActorID: in.Actor, CreatedAt: now,
		}
		opIn := dbalance.Operation{
			ID: d.newOperationID(), NodeID: to2, Kind: dbalance.TransferIn, Amount: req.amount,
			BalanceAfter: to.Amount, TransferID: &tid, CounterpartyNodeID: &from2,
			Comment: req.comment, ActorID: in.Actor, CreatedAt: now,
		}
		for _, b := range sortedByID(from, to) {
			if err := d.Balances.Save(ctx, tx, b); err != nil {
				return err
			}
		}
		for _, op := range []dbalance.Operation{opOut, opIn} {
			if err := d.Ops.Insert(ctx, tx, op); err != nil {
				return err
			}
		}
		for _, op := range []dbalance.Operation{opOut, opIn} {
			if err := d.Audit.Write(ctx, tx, port.AuditEntry{
				Action: "balance.transferred", NodeID: op.NodeID, TargetID: uuid.UUID(op.NodeID),
				ActorID: in.Actor, RequestID: in.RequestID,
				After: map[string]string{
					"transfer_id":          uuid.UUID(tid).String(),
					"operation_id":         opUUID(op.ID).String(),
					"direction":            string(op.Kind),
					"amount":               op.Amount.String(),
					"balance_after":        op.BalanceAfter.String(),
					"counterparty_node_id": op.CounterpartyNodeID.String(),
				},
			}); err != nil {
				return err
			}
		}
		out = TransferOutput{TransferID: tid, Out: opOut, In: opIn}
		return nil
	})
	if err != nil {
		return TransferOutput{}, err
	}
	return out, nil
}

// lockAscending locks the balances of both nodes in ascending node id order.
func lockAscending(ctx context.Context, d Deps, tx port.Tx, now time.Time, a, b dnode.ID) (map[dnode.ID]dbalance.Balance, error) {
	ids := []dnode.ID{a, b}
	sort.Slice(ids, func(i, j int) bool { return lessID(ids[i], ids[j]) })
	out := make(map[dnode.ID]dbalance.Balance, 2)
	for _, id := range ids {
		bal, err := d.Balances.LockForUpdate(ctx, tx, id, now)
		if err != nil {
			return nil, err
		}
		out[id] = bal
	}
	return out, nil
}

func sortedByID(a, b dbalance.Balance) []dbalance.Balance {
	if lessID(b.NodeID, a.NodeID) {
		return []dbalance.Balance{b, a}
	}
	return []dbalance.Balance{a, b}
}

func lessID(a, b dnode.ID) bool {
	x, y := uuid.UUID(a), uuid.UUID(b)
	return bytes.Compare(x[:], y[:]) < 0
}
