package httpapi

import (
	"encoding/json"
	"fmt"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Route templates of the operations that need an idempotency fingerprint. They
// are part of the request hash, so one body sent to another route is another
// request (EC-69).
const (
	routeAddFunds = "/nodes/{id}/credits"
	routeSubFunds = "/nodes/{id}/debits"
	routeTransfer = "/transfers"
)

// operationDTO maps a ledger record to the contract type. Money is a string,
// times are UTC, an empty comment is null.
func operationDTO(op dbalance.Operation) gen.Operation {
	out := gen.Operation{
		Id: uid(op.ID), NodeId: uid(op.NodeID), Kind: gen.OperationKind(op.Kind),
		Amount: op.Amount.String(), BalanceAfter: op.BalanceAfter.String(),
		ActorId: uid(op.ActorID), CreatedAt: op.CreatedAt.UTC(),
		Comment: commentDTO(op.Comment),
	}
	if op.TransferID != nil {
		t := uid(*op.TransferID)
		out.TransferId = &t
	}
	if op.CounterpartyNodeID != nil {
		c := uid(*op.CounterpartyNodeID)
		out.CounterpartyNodeId = &c
	}
	return out
}

func commentDTO(c dbalance.Comment) *string {
	if c.String() == "" {
		return nil
	}
	s := c.String()
	return &s
}

// operationListItemDTO is an operation with the name of its node.
func operationListItemDTO(rec dbalance.Operation, nodeName string) gen.OperationListItem {
	op := operationDTO(rec)
	return gen.OperationListItem{
		Id: op.Id, NodeId: op.NodeId, NodeName: nodeName, Kind: op.Kind, Amount: op.Amount,
		BalanceAfter: op.BalanceAfter, TransferId: op.TransferId, CounterpartyNodeId: op.CounterpartyNodeId,
		Comment: op.Comment, ActorId: op.ActorId, CreatedAt: op.CreatedAt,
	}
}

// replayedHeader is the value of Idempotent-Replayed, nil on the first answer.
func replayedHeader(replayed bool) *string {
	if !replayed {
		return nil
	}
	v := "true"
	return &v
}

// operateFingerprint is the meaning of a credit or a debit: the route, the
// node and the normalized body (amount and comment, an absent comment is the
// empty one).
func operateFingerprint(route, nodeID string, body *gen.OperationRequest) (Fingerprint, error) {
	raw, err := json.Marshal(map[string]string{"amount": body.Amount, "comment": deref(body.Comment, "")})
	if err != nil {
		return Fingerprint{}, fmt.Errorf("fingerprint: %w", err)
	}
	return Fingerprint{Method: "POST", Route: route, PathParams: map[string]string{"id": nodeID}, Body: raw}, nil
}

// transferFingerprint is the meaning of a transfer: the route and the
// normalized body (both nodes, amount, comment).
func transferFingerprint(body *gen.TransferRequest) (Fingerprint, error) {
	raw, err := json.Marshal(map[string]string{
		"fromNodeId": body.FromNodeId.String(), "toNodeId": body.ToNodeId.String(),
		"amount": body.Amount, "comment": deref(body.Comment, ""),
	})
	if err != nil {
		return Fingerprint{}, fmt.Errorf("fingerprint: %w", err)
	}
	return Fingerprint{Method: "POST", Route: routeTransfer, Body: raw}, nil
}

// auditEntryDTO maps a journal record. Before and After are null when empty;
// the actor e-mail is null when the actor is not visible to the reader.
func auditEntryDTO(r port.AuditRecord) gen.AuditEntry {
	e := r.Entry
	out := gen.AuditEntry{
		Id: uid(e.ID()), OccurredAt: e.OccurredAt().UTC(), ActorId: uid(e.ActorID()), NodeId: uid(e.NodeID()),
		NodeName: r.NodeName, Action: gen.AuditAction(e.Action()), TargetType: gen.AuditTargetType(e.TargetType()),
		TargetId: e.TargetID(), Before: fieldsDTO(e.Before()), After: fieldsDTO(e.After()), RequestId: e.RequestID(),
	}
	if r.ActorEmail != "" {
		email := r.ActorEmail
		out.ActorEmail = &email
	}
	return out
}

func fieldsDTO(f audit.Fields) *map[string]string {
	if len(f) == 0 {
		return nil
	}
	m := make(map[string]string, len(f))
	for _, x := range f {
		m[x.Key] = x.Value
	}
	return &m
}
