package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vdao/webinar_mtx/internal/model"
)

func scanWebhook(row interface{ Scan(...any) error }) (*model.Webhook, error) {
	var w model.Webhook
	err := row.Scan(&w.ID, &w.Name, &w.Secret, &w.EmailPath, &w.ProductPath,
		&w.DefaultWebinarID, &w.IsActive, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

const webhookCols = `id, name, secret, email_path, product_path, default_webinar_id, is_active, created_at`

// CreateWebhook inserts a new webhook.
func (s *Store) CreateWebhook(ctx context.Context, w *model.Webhook) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO webhooks (id, name, secret, email_path, product_path, default_webinar_id, is_active)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.ID, w.Name, w.Secret, w.EmailPath, w.ProductPath, w.DefaultWebinarID, w.IsActive)
	return err
}

// UpdateWebhook updates mutable fields.
func (s *Store) UpdateWebhook(ctx context.Context, w *model.Webhook) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE webhooks SET name=$2, email_path=$3, product_path=$4, default_webinar_id=$5, is_active=$6
		 WHERE id=$1`,
		w.ID, w.Name, w.EmailPath, w.ProductPath, w.DefaultWebinarID, w.IsActive)
	return err
}

// WebhookRegenSecret replaces the webhook secret.
func (s *Store) WebhookRegenSecret(ctx context.Context, id, secret string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE webhooks SET secret=$2 WHERE id=$1`, id, secret)
	return err
}

// WebhookByID fetches one webhook.
func (s *Store) WebhookByID(ctx context.Context, id string) (*model.Webhook, error) {
	return scanWebhook(s.DB.QueryRowContext(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE id=$1`, id))
}

// ListWebhooks returns all webhooks.
func (s *Store) ListWebhooks(ctx context.Context) ([]*model.Webhook, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+webhookCols+` FROM webhooks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWebhook removes a webhook (cascade removes its product mappings).
func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM webhooks WHERE id=$1`, id)
	return err
}

// ReplaceWebhookProducts atomically swaps the product→webinar mapping list.
func (s *Store) ReplaceWebhookProducts(ctx context.Context, webhookID string, products []model.WebhookProduct) error {
	return withTx(ctx, s.DB, func(q execer) error {
		if _, err := q.ExecContext(ctx,
			`DELETE FROM webhook_products WHERE webhook_id=$1`, webhookID); err != nil {
			return err
		}
		for _, p := range products {
			if _, err := q.ExecContext(ctx,
				`INSERT INTO webhook_products (webhook_id, external_id, webinar_id) VALUES ($1,$2,$3)`,
				webhookID, p.ExternalID, p.WebinarID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListWebhookProducts returns the product mapping of a webhook.
func (s *Store) ListWebhookProducts(ctx context.Context, webhookID string) ([]model.WebhookProduct, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT webhook_id, external_id, webinar_id FROM webhook_products
		 WHERE webhook_id=$1 ORDER BY external_id`, webhookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WebhookProduct
	for rows.Next() {
		var p model.WebhookProduct
		if err := rows.Scan(&p.WebhookID, &p.ExternalID, &p.WebinarID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FindWebhookProduct looks up a webinar for an external product id.
func (s *Store) FindWebhookProduct(ctx context.Context, webhookID, externalID string) (string, error) {
	var webinarID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT webinar_id FROM webhook_products WHERE webhook_id=$1 AND external_id=$2`,
		webhookID, externalID).Scan(&webinarID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return webinarID, err
}
