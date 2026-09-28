package mfa

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image/png"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/crypto"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUnavailable      = errors.New("TOTP encryption unavailable")
	ErrNotLocal         = errors.New("active local account required")
	ErrInvalidCode      = errors.New("invalid code")
	ErrInvalidChallenge = errors.New("invalid challenge")
	ErrRateLimited      = errors.New("too many verification attempts")
	ErrAlreadyEnabled   = errors.New("TOTP already enabled")
	ErrNotEnabled       = errors.New("TOTP not enabled")
)

const (
	PurposeTOTP           = "totp"
	PurposePasswordChange = "password_change"
)

type Status struct {
	Local             bool `json:"local"`
	Enabled           bool `json:"enabled"`
	RecoveryRemaining int  `json:"recovery_remaining"`
}
type SetupResult struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     string `json:"qr"`
}
type Service struct {
	db        *pgxpool.Pool
	encryptor *crypto.Encryptor
	issuer    string
	now       func() time.Time
}

func NewService(db *pgxpool.Pool, encryptor *crypto.Encryptor, issuer string) *Service {
	if issuer == "" {
		issuer = "Marmot"
	}
	return &Service{db: db, encryptor: encryptor, issuer: issuer, now: time.Now}
}
func (s *Service) Status(ctx context.Context, id string) (Status, error) {
	var out Status
	err := s.db.QueryRow(ctx, `SELECT active AND COALESCE(password_hash,'') <> '', COALESCE(t.confirmed_at IS NOT NULL,false), (SELECT count(*) FROM user_totp_recovery WHERE user_id=u.id AND used_at IS NULL) FROM users u LEFT JOIN user_totp t ON t.user_id=u.id WHERE u.id=$1`, id).Scan(&out.Local, &out.Enabled, &out.RecoveryRemaining)
	return out, err
}

type account struct {
	username, password string
	mustChange         bool
	invalidated        *time.Time
}

// All mutations lock the account first, including challenge consumption and recovery use.
func (s *Service) transaction(ctx context.Context, id string, fn func(pgx.Tx, account) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var a account
	var active bool
	err = tx.QueryRow(ctx, `SELECT username,COALESCE(password_hash,''),active,must_change_password,sessions_invalidated_at FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&a.username, &a.password, &active, &a.mustChange, &a.invalidated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotLocal
	}
	if err != nil {
		return err
	}
	if !active || a.password == "" {
		return ErrNotLocal
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_totp(user_id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	err = fn(tx, a)
	if err != nil && !errors.Is(err, ErrInvalidCode) && !errors.Is(err, ErrRateLimited) {
		return err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return commitErr
	}
	return err
}
func (s *Service) Setup(ctx context.Context, id string) (SetupResult, error) {
	var out SetupResult
	if s.encryptor == nil {
		return out, ErrUnavailable
	}
	err := s.transaction(ctx, id, func(tx pgx.Tx, a account) error {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT confirmed_at IS NOT NULL FROM user_totp WHERE user_id=$1`, id).Scan(&enabled); err != nil {
			return err
		}
		if enabled {
			return ErrAlreadyEnabled
		}
		key, err := totp.Generate(totp.GenerateOpts{Issuer: s.issuer, AccountName: a.username, SecretSize: 20, Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return err
		}
		ciphertext, err := s.encryptor.EncryptString(key.Secret())
		if err != nil {
			return err
		}
		img, err := key.Image(256, 256)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err = png.Encode(&buf, img); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE user_totp SET pending_ciphertext=$2,pending_until=$3 WHERE user_id=$1`, id, ciphertext, s.now().Add(10*time.Minute))
		if err != nil {
			return err
		}
		out = SetupResult{key.Secret(), key.URL(), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}
		return nil
	})
	return out, err
}
func (s *Service) checkLimit(ctx context.Context, tx pgx.Tx, id string) error {
	var n int
	var start *time.Time
	if err := tx.QueryRow(ctx, `SELECT failed_attempts,failure_window FROM user_totp WHERE user_id=$1`, id).Scan(&n, &start); err != nil {
		return err
	}
	if start != nil && s.now().Before(start.Add(5*time.Minute)) && n >= 5 {
		return ErrRateLimited
	}
	return nil
}
func (s *Service) failed(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE user_totp SET failed_attempts=CASE WHEN failure_window IS NULL OR failure_window <= $2 THEN 1 ELSE failed_attempts+1 END, failure_window=CASE WHEN failure_window IS NULL OR failure_window <= $2 THEN $3 ELSE failure_window END WHERE user_id=$1`, id, s.now().Add(-5*time.Minute), s.now())
	if err != nil {
		return err
	}
	return ErrInvalidCode
}
func (s *Service) verify(ctx context.Context, tx pgx.Tx, id, code string, pending bool) error {
	if s.encryptor == nil {
		return ErrUnavailable
	}
	if err := s.checkLimit(ctx, tx, id); err != nil {
		return err
	}
	var secret *string
	var until *time.Time
	var last int64
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT CASE WHEN $2 THEN pending_ciphertext ELSE secret_ciphertext END,pending_until,last_step,confirmed_at IS NOT NULL FROM user_totp WHERE user_id=$1`, id, pending).Scan(&secret, &until, &last, &enabled); err != nil {
		return err
	}
	if pending && enabled {
		return ErrAlreadyEnabled
	}
	if !pending && !enabled {
		return ErrNotEnabled
	}
	if secret == nil || (pending && (until == nil || !s.now().Before(*until))) {
		return s.failed(ctx, tx, id)
	}
	plaintext, err := s.encryptor.DecryptString(*secret)
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	step := s.now().Unix() / 30
	if len(code) == 6 {
		for _, candidate := range []int64{step, step - 1, step + 1} {
			if !pending && candidate <= last {
				continue
			}
			expected, err := totp.GenerateCodeCustom(plaintext, time.Unix(candidate*30, 0), totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
			if err != nil {
				return err
			}
			if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
				_, err = tx.Exec(ctx, `UPDATE user_totp SET last_step=$2,failed_attempts=0,failure_window=NULL WHERE user_id=$1`, id, candidate)
				return err
			}
		}
	}
	if !pending && len(code) == 32 {
		rows, err := tx.Query(ctx, `SELECT id,code_hash FROM user_totp_recovery WHERE user_id=$1 AND used_at IS NULL`, id)
		if err != nil {
			return err
		}
		var match int64
		for rows.Next() {
			var rid int64
			var hash string
			if err = rows.Scan(&rid, &hash); err != nil {
				rows.Close()
				return err
			}
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte(strings.ToLower(code))) == nil {
				match = rid
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if match != 0 {
			if _, err = tx.Exec(ctx, `UPDATE user_totp_recovery SET used_at=$2 WHERE id=$1`, match, s.now()); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE user_totp SET failed_attempts=0,failure_window=NULL WHERE user_id=$1`, id)
			return err
		}
	}
	return s.failed(ctx, tx, id)
}
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (s *Service) recovery(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM user_totp_recovery WHERE user_id=$1`, id); err != nil {
		return nil, err
	}
	codes := make([]string, 10)
	for i := range codes {
		code, err := randomToken(16)
		if err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_totp_recovery(user_id,code_hash) VALUES($1,$2)`, id, string(hash)); err != nil {
			return nil, err
		}
		codes[i] = code
	}
	return codes, nil
}
func invalidate(ctx context.Context, tx pgx.Tx, id string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE users SET sessions_invalidated_at=$2 WHERE id=$1`, id, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM user_auth_challenges WHERE user_id=$1`, id)
	return err
}
func (s *Service) Confirm(ctx context.Context, id, code string) ([]string, error) {
	var codes []string
	err := s.transaction(ctx, id, func(tx pgx.Tx, _ account) error {
		if err := s.verify(ctx, tx, id, code, true); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE user_totp SET secret_ciphertext=pending_ciphertext,pending_ciphertext=NULL,pending_until=NULL,confirmed_at=$2 WHERE user_id=$1`, id, s.now()); err != nil {
			return err
		}
		var err error
		codes, err = s.recovery(ctx, tx, id)
		if err != nil {
			return err
		}
		return invalidate(ctx, tx, id, s.now())
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}
func (s *Service) Disable(ctx context.Context, id, code string) error {
	return s.transaction(ctx, id, func(tx pgx.Tx, _ account) error {
		if err := s.verify(ctx, tx, id, code, false); err != nil {
			return err
		}
		return s.clear(ctx, tx, id)
	})
}
func (s *Service) Reset(ctx context.Context, id string) error {
	return s.transaction(ctx, id, func(tx pgx.Tx, _ account) error { return s.clear(ctx, tx, id) })
}
func (s *Service) clear(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `UPDATE user_totp SET secret_ciphertext=NULL,pending_ciphertext=NULL,pending_until=NULL,confirmed_at=NULL,last_step=-1 WHERE user_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_totp_recovery WHERE user_id=$1`, id); err != nil {
		return err
	}
	return invalidate(ctx, tx, id, s.now())
}
func (s *Service) RegenerateRecovery(ctx context.Context, id, code string) ([]string, error) {
	var codes []string
	err := s.transaction(ctx, id, func(tx pgx.Tx, _ account) error {
		if err := s.verify(ctx, tx, id, code, false); err != nil {
			return err
		}
		var err error
		codes, err = s.recovery(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}
func (s *Service) IssueChallenge(ctx context.Context, id, purpose string) (string, error) {
	if purpose != PurposeTOTP && purpose != PurposePasswordChange {
		return "", ErrInvalidChallenge
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(token))
	err = s.transaction(ctx, id, func(tx pgx.Tx, a account) error {
		if purpose == PurposePasswordChange && !a.mustChange {
			return ErrInvalidChallenge
		}
		if purpose == PurposeTOTP {
			var enabled bool
			if err := tx.QueryRow(ctx, `SELECT confirmed_at IS NOT NULL FROM user_totp WHERE user_id=$1`, id).Scan(&enabled); err != nil {
				return err
			}
			if !enabled || a.mustChange {
				return ErrInvalidChallenge
			}
		}
		fingerprint := sha256.Sum256([]byte(a.password))
		if _, err := tx.Exec(ctx, `DELETE FROM user_auth_challenges WHERE user_id=$1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_auth_challenges(token_hash,user_id,purpose,password_fingerprint,issued_at,expires_at,session_epoch) VALUES($1,$2,$3,$4,$5,$6,$7)`, hash[:], id, purpose, fingerprint[:], s.now(), s.now().Add(5*time.Minute), a.invalidated)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}
func (s *Service) consume(ctx context.Context, token, purpose, code string) (string, error) {
	if len(token) != 64 {
		return "", ErrInvalidChallenge
	}
	hash := sha256.Sum256([]byte(token))
	var id string
	err := s.db.QueryRow(ctx, `SELECT user_id FROM user_auth_challenges WHERE token_hash=$1`, hash[:]).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidChallenge
	}
	if err != nil {
		return "", err
	}
	err = s.transaction(ctx, id, func(tx pgx.Tx, a account) error {
		var storedPurpose string
		var expires time.Time
		var epoch *time.Time
		var fingerprint []byte
		err := tx.QueryRow(ctx, `SELECT purpose,expires_at,session_epoch,password_fingerprint FROM user_auth_challenges WHERE token_hash=$1 FOR UPDATE`, hash[:]).Scan(&storedPurpose, &expires, &epoch, &fingerprint)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidChallenge
		}
		if err != nil {
			return err
		}
		current := sha256.Sum256([]byte(a.password))
		if storedPurpose != purpose || !s.now().Before(expires) || ((a.invalidated == nil) != (epoch == nil)) || (epoch != nil && a.invalidated != nil && !epoch.Equal(*a.invalidated)) || subtle.ConstantTimeCompare(current[:], fingerprint) != 1 {
			return ErrInvalidChallenge
		}
		if purpose == PurposeTOTP {
			if a.mustChange {
				return ErrInvalidChallenge
			}
			if err = s.verify(ctx, tx, id, code, false); err != nil {
				return err
			}
		} else if !a.mustChange {
			return ErrInvalidChallenge
		}
		_, err = tx.Exec(ctx, `DELETE FROM user_auth_challenges WHERE token_hash=$1`, hash[:])
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}
func (s *Service) ConsumePasswordChallenge(ctx context.Context, token string) (string, error) {
	return s.consume(ctx, token, PurposePasswordChange, "")
}
func (s *Service) VerifyLogin(ctx context.Context, token, code string) (string, error) {
	return s.consume(ctx, token, PurposeTOTP, code)
}
