package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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

// CleanupExpired removes stale sessions; called periodically.
func (s *Store) CleanupExpired(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
}
