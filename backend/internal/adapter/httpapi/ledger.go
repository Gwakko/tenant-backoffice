package httpapi

import (
	"log/slog"

	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// LedgerDeps are the collaborators of LedgerHandlers: the use cases of
// balances, the ledger and the audit journal.
type LedgerDeps struct {
	Log     *slog.Logger
	Cursors *CursorCodec
	// Currency is the ISO 4217 code of the installation, shown with balances.
	Currency string

	GetBalance     *balance.GetBalance
	Credit         *balance.Credit
	Debit          *balance.Debit
	Transfer       *balance.Transfer
	ListOperations *balance.ListOperations
	GetOperation   *balance.GetOperation

	ListAudit *audit.ListAudit
}

// LedgerHandlers implements the operations of balances, operations and audit
// (spec 03 sections 5.5 and 5.7). The composition root embeds it next to the
// other handler groups.
type LedgerHandlers struct {
	d LedgerDeps
}

// LedgerOperations are the operation ids LedgerHandlers implements; they go
// into Options.Implemented.
func LedgerOperations() map[string]bool {
	ids := []string{"GetBalance", "CreditNode", "DebitNode", "CreateTransfer", "ListOperations", "GetOperation", "ListAudit"}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// NewLedgerHandlers builds the handlers; every collaborator is required.
func NewLedgerHandlers(d LedgerDeps) (*LedgerHandlers, error) {
	ok := d.Log != nil && d.Cursors != nil && len(d.Currency) == 3 &&
		d.GetBalance != nil && d.Credit != nil && d.Debit != nil && d.Transfer != nil &&
		d.ListOperations != nil && d.GetOperation != nil && d.ListAudit != nil
	if !ok {
		return nil, errMissingDep
	}
	return &LedgerHandlers{d: d}, nil
}
