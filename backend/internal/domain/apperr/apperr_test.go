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
