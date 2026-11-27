package httpapi

import (
	"context"
	"strconv"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/report"
)

// ReportDeps are the collaborators of ReportHandlers.
type ReportDeps struct {
	Cursors *CursorCodec

	Summary  *report.SubtreeSummary
	Children *report.ChildrenBreakdown
}

// ReportHandlers implements the report operations. It is a separate type that
// the composition root embeds next to the other groups.
type ReportHandlers struct {
	d ReportDeps
}

// ReportOperations are the operation ids ReportHandlers implements; they go
// into Options.Implemented.
func ReportOperations() map[string]bool {
	return map[string]bool{"GetSummaryReport": true, "GetChildrenReport": true}
}

// NewReportHandlers builds the handlers; every collaborator is required.
func NewReportHandlers(d ReportDeps) (*ReportHandlers, error) {
	if d.Cursors == nil || d.Summary == nil || d.Children == nil {
		return nil, errMissingDep
	}
	return &ReportHandlers{d: d}, nil
}

// GetSummaryReport returns the totals and the series of the subtree of a node.
func (h *ReportHandlers) GetSummaryReport(ctx context.Context, req gen.GetSummaryReportRequestObject) (gen.GetSummaryReportResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	in := report.SubtreeSummaryInput{
		Actor: a, NodeID: nodeID(req.Id), From: req.Params.From, To: req.Params.To, TZ: deref(req.Params.Tz, ""),
	}
	if req.Params.Granularity != nil {
		in.Granularity = string(*req.Params.Granularity)
	}
	for _, k := range deref(req.Params.Kind, nil) {
		in.Kinds = append(in.Kinds, string(k))
	}
	out, err := h.d.Summary.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return gen.GetSummaryReport200JSONResponse{Body: gen.SubtreeSummary{
		ScopeNodeId: uid(out.ScopeNodeID), Tz: out.TZ, Totals: totalsDTO(out.Totals), Series: periodTotalsDTOs(out.Series),
	}}, nil
}

// GetChildrenReport returns the totals and the balance of every direct child.
func (h *ReportHandlers) GetChildrenReport(ctx context.Context, req gen.GetChildrenReportRequestObject) (gen.GetChildrenReportResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := ParseLimit(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	includeInactive := deref(req.Params.IncludeInactive, true)
	// The cursor is valid for this node, period, zone and filter only.
	bind := CursorBinding{
		Scope: scopeReportChildren + ":" + req.Id.String(),
		Filters: map[string]string{
			"from": req.Params.From, "to": req.Params.To, "tz": deref(req.Params.Tz, ""),
			"includeInactive": strconv.FormatBool(includeInactive),
		},
	}
	after, err := h.d.Cursors.decodePageCursor(req.Params.Cursor, bind)
	if err != nil {
		return nil, err
	}
	out, err := h.d.Children.Execute(ctx, report.ChildrenBreakdownInput{
		Actor: a, NodeID: nodeID(req.Id), From: req.Params.From, To: req.Params.To, TZ: deref(req.Params.Tz, ""),
		IncludeInactive: includeInactive, Limit: limit, Cursor: after,
	})
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	return gen.GetChildrenReport200JSONResponse{Body: gen.ChildTotalsPage{Items: childTotalsDTOs(out.Items), NextCursor: next}}, nil
}
