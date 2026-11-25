package postgres

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

func capture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestMapWriteError(t *testing.T) {
	notFound := apperr.NotFoundError("node")

	t.Run("42501 and 23514 become not_found and are logged at error level", func(t *testing.T) {
		for _, code := range []string{"42501", "23514", "23503"} {
			log, buf := capture()
			err := mapWriteError(log, "insert node", "node", fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, Message: "secret reason"}))
			require.Equal(t, notFound, err, code)
			require.NotContains(t, err.Error(), "secret reason", "the cause must not reach the client")
			require.Contains(t, buf.String(), `"level":"ERROR"`, code)
			require.Contains(t, buf.String(), "secret reason", code)
			require.Contains(t, buf.String(), `"sqlstate":"`+code+`"`)
		}
	})

	t.Run("unique violations map to domain errors", func(t *testing.T) {
		log, buf := capture()
		err := mapWriteError(log, "insert node", "node", &pgconn.PgError{Code: "23505", ConstraintName: "nodes_parent_name_key"})
		require.ErrorIs(t, err, apperr.New(apperr.NameTaken, ""))
		err = mapWriteError(log, "insert node", "node", &pgconn.PgError{Code: "23505", ConstraintName: "nodes_single_platform"})
		require.ErrorIs(t, err, apperr.New(apperr.InvalidParentType, ""))
		require.Empty(t, buf.String(), "expected conflicts are not errors of the use case")
	})

	t.Run("a domain error passes unchanged", func(t *testing.T) {
		log, _ := capture()
		in := apperr.New(apperr.NodeInactive, "x")
		require.Equal(t, in, mapWriteError(log, "op", "node", in))
		require.NoError(t, mapWriteError(log, "op", "node", nil))
	})
}

func TestMapError(t *testing.T) {
	t.Run("42501 is not_found with an error log", func(t *testing.T) {
		log, buf := capture()
		err := mapError(log, "get", "node", &pgconn.PgError{Code: "42501", Message: "permission denied"})
		require.Equal(t, apperr.NotFoundError("node"), err)
		require.Contains(t, buf.String(), `"level":"ERROR"`)
	})

	t.Run("lost connection is unavailable", func(t *testing.T) {
		for _, err := range []error{
			&pgconn.PgError{Code: "57P01"},
			&pgconn.PgError{Code: "08006"},
			io.ErrUnexpectedEOF,
			fmt.Errorf("w: %w", &net.OpError{Op: "read", Err: errors.New("reset")}),
		} {
			log, _ := capture()
			require.Equal(t, apperr.UnavailableError(), mapError(log, "op", "node", err), "%v", err)
		}
	})

	t.Run("anything else is wrapped, not shown as a domain error", func(t *testing.T) {
		log, buf := capture()
		raw := &pgconn.PgError{Code: "23505", ConstraintName: "nodes_pkey"}
		err := mapError(log, "op", "node", raw)
		require.ErrorIs(t, err, raw)
		_, isDomain := apperr.CodeOf(err)
		require.False(t, isDomain)
		require.Empty(t, buf.String())
	})
}

func TestPathText(t *testing.T) {
	p, err := parsePath("0198f6c4a2b37d5e8f1a2b3c4d5e6f70.0198f6c4a2b37d5e8f1a2b3c4d5e6f71")
	require.NoError(t, err)
	require.Len(t, p, 2)
	require.Equal(t, "0198f6c4a2b37d5e8f1a2b3c4d5e6f70.0198f6c4a2b37d5e8f1a2b3c4d5e6f71", pathText(p))
	_, err = parsePath("not-an-id")
	require.Error(t, err)
	_, err = parsePath("")
	require.Error(t, err)
}

func TestConfigValidate(t *testing.T) {
	ok := Config{URL: "postgres://x", MaxConns: 1, StatementTimeout: 1, IdleInTxTimeout: 1}
	require.NoError(t, ok.Validate())
	for name, mut := range map[string]func(*Config){
		"url":       func(c *Config) { c.URL = "" },
		"conns":     func(c *Config) { c.MaxConns = 0 },
		"statement": func(c *Config) { c.StatementTimeout = 0 },
		"idle":      func(c *Config) { c.IdleInTxTimeout = 0 },
	} {
		c := ok
		mut(&c)
		require.Error(t, c.Validate(), name)
	}
}
