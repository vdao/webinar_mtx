// Package model defines the database entities.
package model

import "time"

// Webinar statuses.
const (
	WebinarDraft     = "draft"
	WebinarScheduled = "scheduled"
	WebinarLive      = "live"
	WebinarEnded     = "ended"
)

// WebinarStatuses is the ordered list of valid webinar statuses.
var WebinarStatuses = []string{WebinarDraft, WebinarScheduled, WebinarLive, WebinarEnded}

// Participant statuses.
const (
	ParticipantActive  = "active"
	ParticipantBlocked = "blocked"
)

// Participant registration sources.
const (
	SourceImport  = "import"
	SourceManual  = "manual"
	SourceWebhook = "webhook"
)

// Webinar is a single webinar room backed by one MediaMTX path w/{StreamKey}.
type Webinar struct {
	ID          string
	Slug        string // public room URL part: /w/{slug}
	Title       string
	Description string
	StreamKey   string // secret MediaMTX path segment
	Status      string // draft | scheduled | live | ended
	ScheduledAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Participant is an email allowed to watch a specific webinar.
type Participant struct {
	ID         string
	WebinarID  string
	Email      string
	Name       string
	Status     string // active | blocked
	Source     string // import | manual | webhook
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

// ParticipantSession is a logged-in viewer session.
type ParticipantSession struct {
	ID            string
	ParticipantID string
	WebinarID     string
	TokenHash     string
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// LoginCode is a one-time email verification code.
type LoginCode struct {
	ID        string
	WebinarID string
	Email     string
	CodeHash  string
	ExpiresAt time.Time
	Attempts  int
	CreatedAt time.Time
}

// Webhook is an inbound payment webhook configuration.
type Webhook struct {
	ID               string
	Name             string
	Secret           string
	EmailPath        string // dot-path to the customer email in the payload
	ProductPath      string // dot-path to the product/tariff id in the payload
	DefaultWebinarID *string
	IsActive         bool
	CreatedAt        time.Time
}

// WebhookProduct maps an external product id to a webinar.
type WebhookProduct struct {
	WebhookID  string
	ExternalID string
	WebinarID  string
}
