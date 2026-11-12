package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vdao/webinar_mtx/internal/model"
)

func scanParticipant(row interface{ Scan(...any) error }) (*model.Participant, error) {
	var p model.Participant
	err := row.Scan(&p.ID, &p.WebinarID, &p.Email, &p.Name, &p.Status, &p.Source,
		&p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const participantCols = `id, webinar_id, email, name, status, source, created_at, last_seen_at`

// AddParticipant inserts a participant; an existing (webinar,email) pair is
// updated (name refreshed, status kept). Returns the stored record and
// whether it was newly created.
func (s *Store) AddParticipant(ctx context.Context, webinarID, email, name, source string) (*model.Participant, bool, error) {
	p := &model.Participant{WebinarID: webinarID, Email: email, Name: name, Source: source}
	var created bool
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO participants (id, webinar_id, email, name, source)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (webinar_id, email) DO UPDATE
		   SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE participants.name END
		 RETURNING id, status, created_at, (xmax = 0)`,
		"p_"+randomID(), webinarID, email, name, source,
	).Scan(&p.ID, &p.Status, &p.CreatedAt, &created)
	if err != nil {
		return nil, false, err
	}
	return p, created, nil
}

// ImportParticipants bulk-inserts participants with source 'import'.
// Returns the number of newly inserted rows (existing ones are skipped).
func (s *Store) ImportParticipants(ctx context.Context, webinarID string, emails, names []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	ids := make([]string, len(emails))
	for i := range ids {
		ids[i] = "p_" + randomID()
	}
	src := make([]string, len(emails))
	for i := range src {
		src[i] = model.SourceImport
	}
	var inserted int
	err := withTx(ctx, s.DB, func(q execer) error {
		rows, err := q.QueryContext(ctx,
			`INSERT INTO participants (id, webinar_id, email, name, source)
			 SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[])
			 ON CONFLICT (webinar_id, email) DO NOTHING
			 RETURNING id`,
			pgArray(ids), expandArg(webinarID, len(emails)), pgArray(emails), pgArray(names), src)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			inserted++
		}
		return rows.Err()
	})
	return inserted, err
}

// ParticipantByID fetches one participant.
func (s *Store) ParticipantByID(ctx context.Context, id string) (*model.Participant, error) {
	return scanParticipant(s.DB.QueryRowContext(ctx,
		`SELECT `+participantCols+` FROM participants WHERE id=$1`, id))
}

// ParticipantByEmail fetches an active participant for a webinar by email.
// Returns ErrNotFound when the email is not registered, and a participant with
// Status=blocked when the participant is blocked.
func (s *Store) ParticipantByEmail(ctx context.Context, webinarID, email string) (*model.Participant, error) {
	p, err := scanParticipant(s.DB.QueryRowContext(ctx,
		`SELECT `+participantCols+` FROM participants WHERE webinar_id=$1 AND email=$2`,
		webinarID, email))
	if err != nil {
		return nil, err
	}
	if p.Status == model.ParticipantBlocked {
		return p, nil // caller decides how to report blocked access
	}
	return p, nil
}

// ListParticipants lists participants of a webinar, optionally filtered by a
// substring of email or name, newest first.
func (s *Store) ListParticipants(ctx context.Context, webinarID, query string, limit int) ([]*model.Participant, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q := `SELECT ` + participantCols + ` FROM participants WHERE webinar_id=$1`
	args := []any{webinarID}
	if query != "" {
		q += ` AND (email ILIKE $2 OR name ILIKE $2)`
		args = append(args, "%"+escapeLike(query)+"%")
	}
	q += ` ORDER BY created_at DESC LIMIT ` + itoa(limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Participant
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountParticipants counts all participants of a webinar.
func (s *Store) CountParticipants(ctx context.Context, webinarID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM participants WHERE webinar_id=$1`, webinarID).Scan(&n)
	return n, err
}

// SetParticipantStatus blocks or unblocks a participant.
func (s *Store) SetParticipantStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE participants SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteParticipant removes a participant.
func (s *Store) DeleteParticipant(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM participants WHERE id=$1`, id)
	return err
}

// TouchParticipant updates last_seen_at for all sessions of a participant.
func (s *Store) TouchParticipant(ctx context.Context, participantID string) {
	_, _ = s.DB.ExecContext(ctx,
		`UPDATE participants SET last_seen_at=now() WHERE id=$1`, participantID)
}

// ---- helpers ----

func withTx(ctx context.Context, db *sql.DB, fn func(q execer) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// pgArray converts a slice into a PostgreSQL text[] literal.
func pgArray(items []string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(it, `"`, `\"`))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// expandArg repeats value n times as a text[] literal, to pair with unnest.
func expandArg(value string, n int) string {
	rep := make([]string, n)
	for i := range rep {
		rep[i] = value
	}
	return pgArray(rep)
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `%`, `\%`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
