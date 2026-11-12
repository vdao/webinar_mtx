package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"strings"

	"github.com/vdao/webinar_mtx/internal/model"
)

// codeAlphabet avoids visually confusable characters (0/O, 1/I/l).
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// genParticipantCode produces a personal access code (~60 bits of entropy
// from crypto/rand — the code is an access credential).
func genParticipantCode() string {
	b := make([]byte, 12)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b[i] = codeAlphabet[n.Int64()]
	}
	return string(b)
}

func scanParticipant(row interface{ Scan(...any) error }) (*model.Participant, error) {
	var p model.Participant
	err := row.Scan(&p.ID, &p.WebinarID, &p.Email, &p.Name, &p.Code, &p.Status, &p.Source,
		&p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const participantCols = `id, webinar_id, email, name, code, status, source, created_at, last_seen_at`

// AddParticipant inserts a participant with a fresh personal code; an
// existing (webinar,email) pair is updated (name refreshed, code and status
// kept). Returns the stored record and whether it was newly created.
func (s *Store) AddParticipant(ctx context.Context, webinarID, email, name, source string) (*model.Participant, bool, error) {
	p := &model.Participant{WebinarID: webinarID, Email: email, Name: name, Source: source}
	var created bool
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO participants (id, webinar_id, email, name, code, source)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (webinar_id, email) DO UPDATE
		   SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE participants.name END
		 RETURNING id, code, status, created_at, (xmax = 0)`,
		"p_"+randomID(), webinarID, email, name, genParticipantCode(), source,
	).Scan(&p.ID, &p.Code, &p.Status, &p.CreatedAt, &created)
	if err != nil {
		return nil, false, err
	}
	return p, created, nil
}

// ImportParticipants bulk-inserts participants with source 'import'.
// Returns the newly created records (existing ones are skipped) with email,
// name and code filled for notification purposes.
func (s *Store) ImportParticipants(ctx context.Context, webinarID string, emails, names []string) ([]*model.Participant, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	ids := make([]string, len(emails))
	codes := make([]string, len(emails))
	for i := range ids {
		ids[i] = "p_" + randomID()
		codes[i] = genParticipantCode()
	}
	src := make([]string, len(emails))
	for i := range src {
		src[i] = model.SourceImport
	}
	var inserted []*model.Participant
	err := withTx(ctx, s.DB, func(q execer) error {
		rows, err := q.QueryContext(ctx,
			`INSERT INTO participants (id, webinar_id, email, name, code, source)
			 SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[], $6::text[])
			 ON CONFLICT (webinar_id, email) DO NOTHING
			 RETURNING email, name, code`,
			pgArray(ids), expandArg(webinarID, len(emails)), pgArray(emails),
			pgArray(names), pgArray(codes), src)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p := &model.Participant{WebinarID: webinarID, Source: model.SourceImport}
			if err := rows.Scan(&p.Email, &p.Name, &p.Code); err != nil {
				return err
			}
			inserted = append(inserted, p)
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

// ParticipantByCode resolves a personal access code. Codes are globally
// unique, so the webinar check guards against a code from another room.
func (s *Store) ParticipantByCode(ctx context.Context, webinarID, code string) (*model.Participant, error) {
	return scanParticipant(s.DB.QueryRowContext(ctx,
		`SELECT `+participantCols+` FROM participants WHERE code=$1 AND webinar_id=$2`,
		code, webinarID))
}

// RegenParticipantCode replaces the personal code (e.g. after a leak).
func (s *Store) RegenParticipantCode(ctx context.Context, id string) (*model.Participant, error) {
	var code string
	err := s.DB.QueryRowContext(ctx,
		`UPDATE participants SET code=$2 WHERE id=$1 RETURNING code`, id, genParticipantCode(),
	).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p, err := s.ParticipantByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Code = code
	return p, nil
}

// ListParticipants lists participants of a webinar, optionally filtered by a
// substring of email, name or code, newest first.
func (s *Store) ListParticipants(ctx context.Context, webinarID, query string, limit int) ([]*model.Participant, error) {
	if limit <= 0 || limit > 20000 {
		limit = 500
	}
	q := `SELECT ` + participantCols + ` FROM participants WHERE webinar_id=$1`
	args := []any{webinarID}
	if query != "" {
		q += ` AND (email ILIKE $2 OR name ILIKE $2 OR code ILIKE $2)`
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
