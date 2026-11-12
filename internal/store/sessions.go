package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/vdao/webinar_mtx/internal/model"
)

func randomID() string {
	buf := make([]byte, 12)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateSession issues a new viewer session for a participant and returns the
// raw token (only the hash is stored).
func (s *Store) CreateSession(ctx context.Context, participantID, webinarID string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, participant_id, webinar_id, token_hash, expires_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		"s_"+randomID(), participantID, webinarID, hashToken(token), time.Now().Add(ttl))
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionByToken resolves a raw token into a valid session joined with the
// participant status. Expired or unknown tokens return ErrNotFound.
func (s *Store) SessionByToken(ctx context.Context, token string) (*model.ParticipantSession, *model.Participant, error) {
	var sess model.ParticipantSession
	var p model.Participant
	err := s.DB.QueryRowContext(ctx,
		`SELECT s.id, s.participant_id, s.webinar_id, s.expires_at, s.created_at,
		        p.id, p.webinar_id, p.email, p.name, p.status, p.source, p.created_at, p.last_seen_at
		 FROM sessions s
		 JOIN participants p ON p.id = s.participant_id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`,
		hashToken(token),
	).Scan(&sess.ID, &sess.ParticipantID, &sess.WebinarID, &sess.ExpiresAt, &sess.CreatedAt,
		&p.ID, &p.WebinarID, &p.Email, &p.Name, &p.Status, &p.Source, &p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return &sess, &p, nil
}

// DeleteSession removes one session by raw token.
func (s *Store) DeleteSession(ctx context.Context, token string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hashToken(token))
}

// DeleteSessionsForParticipant removes all sessions of a participant (logout everywhere).
func (s *Store) DeleteSessionsForParticipant(ctx context.Context, participantID string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE participant_id=$1`, participantID)
}

// ---- one-time login codes ----

// CreateLoginCode stores a fresh one-time code for (webinar,email), replacing
// previous ones, and returns the plain code.
func (s *Store) CreateLoginCode(ctx context.Context, webinarID, email string, ttl time.Duration) (string, error) {
	buf := make([]byte, 4)
	rand.Read(buf)
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint32(buf)%1000000)
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM login_codes WHERE webinar_id=$1 AND email=$2`, webinarID, email)
	if err != nil {
		return "", err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO login_codes (id, webinar_id, email, code_hash, expires_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		"c_"+randomID(), webinarID, email, hashToken(code), time.Now().Add(ttl))
	if err != nil {
		return "", err
	}
	return code, nil
}

var ErrBadCode = errors.New("invalid or expired code")

// ConsumeLoginCode verifies a submitted code with brute-force protection.
func (s *Store) ConsumeLoginCode(ctx context.Context, webinarID, email, code string) error {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, code_hash, expires_at, attempts FROM login_codes
		 WHERE webinar_id=$1 AND email=$2 ORDER BY created_at DESC LIMIT 1`,
		webinarID, email)
	var id, storedHash string
	var expires time.Time
	var attempts int
	if err := row.Scan(&id, &storedHash, &expires, &attempts); err != nil {
		return ErrBadCode
	}
	if time.Now().After(expires) || attempts >= 5 {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_codes WHERE id=$1`, id)
		return ErrBadCode
	}
	if hashToken(code) != storedHash {
		_, _ = s.DB.ExecContext(ctx,
			`UPDATE login_codes SET attempts=attempts+1 WHERE id=$1`, id)
		return ErrBadCode
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_codes WHERE id=$1`, id)
	return nil
}

// CleanupExpired removes stale sessions and codes; called periodically.
func (s *Store) CleanupExpired(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_codes WHERE expires_at < now() - interval '1 hour'`)
}
