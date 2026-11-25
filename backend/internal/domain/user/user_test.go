package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

func TestParseID(t *testing.T) {
	id, err := user.ParseID("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	require.NoError(t, err)
	require.Equal(t, "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", id.String())
	require.False(t, id.IsZero())
	_, err = user.ParseID("nope")
	require.Error(t, err)
}

func TestActorIDIsUserID(t *testing.T) {
	actor := user.ActorID(user.ID{})
	require.True(t, user.ID(actor).IsZero())
}

func TestIsActive(t *testing.T) {
	require.True(t, user.User{Status: user.StatusActive}.IsActive())
	require.False(t, user.User{Status: user.StatusInactive}.IsActive())
}
