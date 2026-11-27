package httpapi

import (
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	drep "github.com/Gwakko/tenant-backoffice/backend/internal/domain/report"
)

// scopeReportChildren names the children report in the cursor binding.
const scopeReportChildren = "report-children"

func totalsDTO(t drep.Totals) gen.Totals {
	return gen.Totals{Count: t.Count, Credits: t.Credits.String(), Debits: t.Debits.String(), Net: t.Net.String()}
}

func periodTotalsDTOs(series []drep.PeriodTotals) []gen.PeriodTotals {
	out := make([]gen.PeriodTotals, len(series))
	for i, p := range series {
		out[i] = gen.PeriodTotals{
			PeriodStart: openapi_types.Date{Time: p.PeriodStart.Time()},
			Count:       p.Count, Credits: p.Credits.String(), Debits: p.Debits.String(), Net: p.Net.String(),
		}
	}
	return out
}

func childTotalsDTOs(items []drep.ChildTotals) []gen.ChildTotals {
	out := make([]gen.ChildTotals, len(items))
	for i, c := range items {
		out[i] = gen.ChildTotals{
			NodeId: uid(c.NodeID), Name: c.Name, Type: gen.NodeType(c.Type), Active: c.Active,
			Count: c.Totals.Count, Credits: c.Totals.Credits.String(), Debits: c.Totals.Debits.String(),
			Net: c.Totals.Net.String(), Balance: c.Balance.String(),
		}
	}
	return out
}
