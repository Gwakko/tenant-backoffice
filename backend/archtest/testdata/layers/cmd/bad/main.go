package main

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/good"
)

func main() { _, _, _ = app.X, config.X, good.T }
