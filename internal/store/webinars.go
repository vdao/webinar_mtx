package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vdao/webinar_mtx/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

func scanWebinar(row interface{ Scan(...any) error }) (*model.Webinar, error) {
	var w model.Webinar
	err := row.Scan(&w.ID, &w.Slug, &w.Title, &w.Description, &w.StreamKey,
		&w.Status, &w.ScheduledAt, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

const webinarCols = `id, slug, title, description, stream_key, status, scheduled_at, created_at, updated_at`

// CreateWebinar inserts a new webinar.
func (s *Store) CreateWebinar(ctx context.Context, w *model.Webinar) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO webinars (id, slug, title, description, stream_key, status, scheduled_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.ID, w.Slug, w.Title, w.Description, w.StreamKey, w.Status, w.ScheduledAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return ErrConflict
	}
	return err
}

// UpdateWebinar updates mutable fields of a webinar.
func (s *Store) UpdateWebinar(ctx context.Context, w *model.Webinar) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE webinars SET slug=$2, title=$3, description=$4, status=$5, scheduled_at=$6, updated_at=now()
		 WHERE id=$1`,
		w.ID, w.Slug, w.Title, w.Description, w.Status, w.ScheduledAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return ErrConflict
	}
	return err
}

// UpdateWebinarStatus sets only the status.
func (s *Store) UpdateWebinarStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE webinars SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RegenStreamKey replaces the stream key with a new one.
func (s *Store) RegenStreamKey(ctx context.Context, id, key string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE webinars SET stream_key=$2, updated_at=now() WHERE id=$1`, id, key)
	return err
}

// WebinarByID fetches one webinar.
func (s *Store) WebinarByID(ctx context.Context, id string) (*model.Webinar, error) {
	return scanWebinar(s.DB.QueryRowContext(ctx,
		`SELECT `+webinarCols+` FROM webinars WHERE id=$1`, id))
}

// WebinarBySlug fetches one webinar by its public room slug.
func (s *Store) WebinarBySlug(ctx context.Context, slug string) (*model.Webinar, error) {
	return scanWebinar(s.DB.QueryRowContext(ctx,
		`SELECT `+webinarCols+` FROM webinars WHERE slug=$1`, slug))
}

// WebinarByStreamKey fetches a webinar by its secret MediaMTX path segment.
func (s *Store) WebinarByStreamKey(ctx context.Context, key string) (*model.Webinar, error) {
	return scanWebinar(s.DB.QueryRowContext(ctx,
		`SELECT `+webinarCols+` FROM webinars WHERE stream_key=$1`, key))
}

// ListWebinars returns all webinars newest first.
func (s *Store) ListWebinars(ctx context.Context) ([]*model.Webinar, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+webinarCols+` FROM webinars ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Webinar
	for rows.Next() {
		w, err := scanWebinar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WebinarParticipantCounts returns participant counts per webinar id.
func (s *Store) WebinarParticipantCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT webinar_id, count(*) FROM participants GROUP BY webinar_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	var id string
	var n int
	for rows.Next() {
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// DeleteWebinar removes a webinar and its dependants (cascade).
func (s *Store) DeleteWebinar(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM webinars WHERE id=$1`, id)
	return err
}

// nowFn allows tests to fake time if needed.
var _ = time.Now
