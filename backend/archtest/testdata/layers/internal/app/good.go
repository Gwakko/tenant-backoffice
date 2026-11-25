package app

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
)

var (
	_ httpapi.X
	_ postgres.X
)
