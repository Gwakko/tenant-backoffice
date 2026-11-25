package postgres

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// SQLSTATE values the adapter reacts to.
const (
	codeInsufficientPrivilege = "42501" // RLS rejected the row or statement
	codeUniqueViolation       = "23505"
	codeForeignKeyViolation   = "23503"
	codeRestrictViolation     = "23001" // ON DELETE RESTRICT
	codeCheckViolation        = "23514"
	codeAdminShutdown         = "57P01"
	codeCrashShutdown         = "57P02"
	codeCannotConnectNow      = "57P03"
)

// Constraint names from migration 0001.
const (
	constraintParentName     = "nodes_parent_name_key"
	constraintSinglePlatform = "nodes_single_platform"
)

func pgError(err error) (*pgconn.PgError, bool) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// hiddenOrRejected reports the two statuses PostgreSQL gives when the actor
// writes under or into a part of the tree it cannot see: 42501 from a policy
// and 23514 from the parent trigger, which reads the parent under the same
// policies and finds nothing.
func hiddenOrRejected(err error) (*pgconn.PgError, bool) {
	pe, ok := pgError(err)
	if !ok {
		return nil, false
	}
	switch pe.Code {
	case codeInsufficientPrivilege, codeCheckViolation, codeForeignKeyViolation:
		return pe, true
	}
	return nil, false
}

// mapWriteError converts an error of a node write into a domain error. A
// rejection by row-level security (or by the parent trigger that runs under it)
// means use-case authorization let something through: the client gets the
// same not_found as for a missing object, and the real reason goes to the log
// at error level.
func mapWriteError(log *slog.Logger, op, what string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[apperr.Error](err); ok {
		return err
	}
	if pe, ok := hiddenOrRejected(err); ok {
		log.Error("storage rejected a write the use case should have prevented",
			"op", op, "sqlstate", pe.Code, "constraint", pe.ConstraintName, "reason", pe.Message)
		return apperr.NotFoundError(what)
	}
	if pe, ok := pgError(err); ok && pe.Code == codeUniqueViolation {
		switch pe.ConstraintName {
		case constraintParentName:
			return apperr.New(apperr.NameTaken, "a sibling with this name already exists")
		case constraintSinglePlatform:
			return apperr.New(apperr.InvalidParentType, "there is exactly one root node")
		}
	}
	return mapError(log, op, what, err)
}

// mapError converts errors every operation can meet: a statement rejected by
// row-level security (42501, logged at error level, reported as not_found), a
// lost or refused connection (unavailable) and anything else (wrapped).
func mapError(log *slog.Logger, op, what string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[apperr.Error](err); ok {
		return err
	}
	if pe, ok := pgError(err); ok {
		if pe.Code == codeInsufficientPrivilege {
			log.Error("storage rejected a statement the use case should have prevented",
				"op", op, "sqlstate", pe.Code, "reason", pe.Message)
			return apperr.NotFoundError(what)
		}
		if connectionLost(pe) {
			log.Warn("storage connection lost", "op", op, "sqlstate", pe.Code)
			return apperr.UnavailableError()
		}
	} else if networkFailure(err) {
		log.Warn("storage connection lost", "op", op)
		return apperr.UnavailableError()
	}
	return fmt.Errorf("postgres: %s: %w", op, err)
}

func connectionLost(pe *pgconn.PgError) bool {
	return strings.HasPrefix(pe.Code, "08") ||
		pe.Code == codeAdminShutdown || pe.Code == codeCrashShutdown || pe.Code == codeCannotConnectNow
}

func networkFailure(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	return false
}
