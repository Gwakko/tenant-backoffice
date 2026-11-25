package bad

import (
	"net/http"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	_ http.Handler
	_ postgres.X
	_ app.X
	_ pgxpool.Pool
)
