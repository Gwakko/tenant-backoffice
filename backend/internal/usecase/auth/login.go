package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// LoginInput is the credentials of UC-A1. IP and UserAgent are for the log
// only; the password never leaves this struct.
type LoginInput struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
}

// Validate rejects what can never be a credential. Every failure is
// invalid_credentials, never validation_failed, so the answer says nothing
// about which part was wrong.
func (in LoginInput) Validate() error {
	if strings.TrimSpace(in.Email) == "" || in.Password == "" || len(in.Password) > maxPasswordBytes {
		return apperr.InvalidCredentialsError()
	}
	return nil
}

// LoginOutput is the new session.
type LoginOutput struct {
	Session
	Email user.Email
}

// LoginUseCase is UC-A1.
type LoginUseCase struct {
	d Deps
	// dummyHash is verified against when the email is unknown, so that an
	// unknown email costs the same as a wrong password. It is made by the real
	// hasher, hence with the real parameters.
	dummyHash user.PasswordHash
}

// NewLoginUseCase builds the use case. It hashes one random password once, for
// the dummy hash.
func NewLoginUseCase(d Deps) (*LoginUseCase, error) {
	if err := d.need(d.Tx != nil && d.Users != nil && d.Refresh != nil && d.Codec != nil &&
		d.Issuer != nil && d.Hasher != nil && d.Clock != nil && d.IDs != nil && d.Config.RefreshTTL > 0); err != nil {
		return nil, err
	}
	var seed [24]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("auth: random seed: %w", err)
	}
	dummy, err := d.Hasher.Hash(hex.EncodeToString(seed[:]))
	if err != nil {
		return nil, fmt.Errorf("auth: dummy hash: %w", err)
	}
	return &LoginUseCase{d: d, dummyHash: dummy}, nil
}

// Execute checks the credentials and opens a session with a new refresh family.
func (uc *LoginUseCase) Execute(ctx context.Context, in LoginInput) (LoginOutput, error) {
	log := uc.d.logger()
	if err := in.Validate(); err != nil {
		log.Info("login rejected", "reason", "malformed", "ip", in.IP)
		return LoginOutput{}, err
	}
	email, err := user.ParseEmail(in.Email)
	if err != nil {
		log.Info("login rejected", "reason", "malformed", "ip", in.IP)
		return LoginOutput{}, apperr.InvalidCredentialsError()
	}

	rec, found, err := uc.d.Users.ByEmail(ctx, email)
	if err != nil {
		return LoginOutput{}, err
	}
	hash := rec.PasswordHash
	if !found {
		hash = uc.dummyHash
	}
	// One verification on every path that got this far: the time of an unknown
	// email, a wrong password and an inactive account does not differ by a hash.
	match, err := uc.d.Hasher.Verify(hash, in.Password)
	if err != nil {
		return LoginOutput{}, fmt.Errorf("auth: verify password: %w", err)
	}
	if !found || !match || !rec.IsActive() {
		log.Info("login rejected", "reason", "credentials", "ip", in.IP)
		return LoginOutput{}, apperr.InvalidCredentialsError()
	}

	now := uc.d.Clock.Now()
	var out LoginOutput
	err = uc.d.Tx.InTx(ctx, rec.ID, port.TxOptions{}, func(ctx context.Context, tx port.Tx) error {
		token, row, err := newRefresh(uc.d, rec.ID, uc.d.IDs.New(), now)
		if err != nil {
			return err
		}
		if err := uc.d.Refresh.Insert(ctx, tx, row); err != nil {
			return err
		}
		at, expiresIn, err := accessFor(uc.d, rec.ID, rec.TokenVersion, now)
		if err != nil {
			return err
		}
		out = LoginOutput{
			Session: Session{
				AccessToken: at.Token, ExpiresIn: expiresIn, RefreshToken: token,
				RefreshExpiresAt: row.ExpiresAt, UserID: rec.ID,
			},
			Email: email,
		}
		return nil
	})
	if err != nil {
		return LoginOutput{}, err
	}
	log.Info("login", "user_id", rec.ID.String(), "ip", in.IP, "user_agent", in.UserAgent)
	return out, nil
}
