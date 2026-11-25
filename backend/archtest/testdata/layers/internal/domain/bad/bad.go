package bad

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var (
	_ context.Context
	_ pgx.Tx
	_ decimal.Decimal
)
