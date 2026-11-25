package apperr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

func TestErrorMatchesByCode(t *testing.T) {
	err := fmt.Errorf("wrap: %w", apperr.New(apperr.NodeInactive, "x").WithField("f", "d"))
	require.True(t, errors.Is(err, apperr.New(apperr.NodeInactive, "")))
	require.False(t, errors.Is(err, apperr.New(apperr.MoveCycle, "")))
	code, ok := apperr.CodeOf(err)
	require.True(t, ok)
	require.Equal(t, apperr.NodeInactive, code)
	_, ok = apperr.CodeOf(errors.New("plain"))
	require.False(t, ok)
	require.Equal(t, "node_inactive: x", apperr.New(apperr.NodeInactive, "x").Error())
}

func TestSharedConstructors(t *testing.T) {
	nf := apperr.NotFoundError("node")
	require.Equal(t, apperr.NotFound, nf.Code)
	require.Equal(t, "node not found", nf.Message)
	require.ErrorIs(t, fmt.Errorf("w: %w", nf), apperr.New(apperr.NotFound, ""))
	require.Equal(t, apperr.Forbidden, apperr.ForbiddenError("x").Code)
	require.Equal(t, apperr.Unavailable, apperr.UnavailableError().Code)
	require.EqualValues(t, "db_unavailable", apperr.Unavailable)
}
