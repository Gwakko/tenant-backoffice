package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// GetBalance returns the balance of a node.
func (h *LedgerHandlers) GetBalance(ctx context.Context, req gen.GetBalanceRequestObject) (gen.GetBalanceResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.GetBalance.Execute(ctx, balance.GetBalanceInput{Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id)})
	if err != nil {
		return nil, err
	}
	return gen.GetBalance200JSONResponse{Body: gen.Balance{
		NodeId: uid(out.NodeID), Amount: out.Amount.String(), Currency: h.d.Currency, UpdatedAt: out.UpdatedAt.UTC(),
	}}, nil
}

// CreditNode adds an amount to the balance of a node.
func (h *LedgerHandlers) CreditNode(ctx context.Context, req gen.CreditNodeRequestObject) (gen.CreditNodeResponseObject, error) {
	out, err := h.operate(ctx, routeAddFunds, req.Id.String(), req.Params.IdempotencyKey, req.Body, h.d.Credit.Execute, nodeID(req.Id))
	if err != nil {
		return nil, err
	}
	return gen.CreditNode201JSONResponse{
		Body: operationDTO(out.Operation), Headers: gen.CreditNode201ResponseHeaders{IdempotentReplayed: replayedHeader(out.Replayed)},
	}, nil
}

// DebitNode subtracts an amount from the balance of a node.
func (h *LedgerHandlers) DebitNode(ctx context.Context, req gen.DebitNodeRequestObject) (gen.DebitNodeResponseObject, error) {
	out, err := h.operate(ctx, routeSubFunds, req.Id.String(), req.Params.IdempotencyKey, req.Body, h.d.Debit.Execute, nodeID(req.Id))
	if err != nil {
		return nil, err
	}
	return gen.DebitNode201JSONResponse{
		Body: operationDTO(out.Operation), Headers: gen.DebitNode201ResponseHeaders{IdempotentReplayed: replayedHeader(out.Replayed)},
	}, nil
}

// operate is the shared part of credit and debit: the actor from the context,
// the fingerprint of the request and the use case call.
func (h *LedgerHandlers) operate(
	ctx context.Context, route, id, key string, body *gen.OperationRequest,
	run func(context.Context, balance.OperateInput) (balance.OperateOutput, error), node dnode.ID,
) (balance.OperateOutput, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return balance.OperateOutput{}, err
	}
	if body == nil {
		return balance.OperateOutput{}, missingBody()
	}
	fp, err := operateFingerprint(route, id, body)
	if err != nil {
		return balance.OperateOutput{}, err
	}
	hash, err := RequestHash(fp)
	if err != nil {
		return balance.OperateOutput{}, err
	}
	return run(ctx, balance.OperateInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: node,
		Amount: body.Amount, Comment: deref(body.Comment, ""), IdempotencyKey: key, RequestHash: hash,
	})
}

// CreateTransfer moves an amount between two nodes; the answer holds both legs.
func (h *LedgerHandlers) CreateTransfer(ctx context.Context, req gen.CreateTransferRequestObject) (gen.CreateTransferResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	fp, err := transferFingerprint(req.Body)
	if err != nil {
		return nil, err
	}
	hash, err := RequestHash(fp)
	if err != nil {
		return nil, err
	}
	out, err := h.d.Transfer.Execute(ctx, balance.TransferInput{
		Actor: a, RequestID: RequestIDFrom(ctx),
		FromNodeID: nodeID(req.Body.FromNodeId), ToNodeID: nodeID(req.Body.ToNodeId),
		Amount: req.Body.Amount, Comment: deref(req.Body.Comment, ""),
		IdempotencyKey: req.Params.IdempotencyKey, RequestHash: hash,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateTransfer201JSONResponse{
		Body: gen.Transfer{
			TransferId: uid(out.TransferID), Out: operationDTO(out.Out), In: operationDTO(out.In),
		},
		Headers: gen.CreateTransfer201ResponseHeaders{IdempotentReplayed: replayedHeader(out.Replayed)},
	}, nil
}
