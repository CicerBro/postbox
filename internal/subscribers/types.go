package subscribers

import (
	"postbox/internal/store"
	"time"
)

type Service struct {
	db      *store.DB
	baseURL string
}

func New(db *store.DB) *Service { return &Service{db: db} }

type Brand struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	FromName         string `json:"from_name"`
	FromEmail        string `json:"from_email"`
	ReplyTo          string `json:"reply_to"`
	ConfigurationSet string `json:"configuration_set"`
	TrackingDomain   string `json:"tracking_domain"`
	CreatedAt        int64  `json:"created_at"`
}
type List struct {
	ID        int64  `json:"id"`
	BrandID   int64  `json:"brand_id"`
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	OptIn     string `json:"opt_in"`
	CreatedAt int64  `json:"created_at"`
}
type Subscriber struct {
	ID                      int64            `json:"id"`
	BrandID                 int64            `json:"brand_id"`
	Email                   string           `json:"email"`
	Name                    string           `json:"name"`
	Fields                  map[string]any   `json:"fields"`
	Status                  string           `json:"status"`
	ConsentAt               *int64           `json:"gdpr_consent_at,omitempty"`
	MarketingUnsubscribedAt *int64           `json:"marketing_unsubscribed_at,omitempty"`
	ConsentIP               string           `json:"gdpr_consent_ip,omitempty"`
	CreatedAt               int64            `json:"created_at"`
	UpdatedAt               int64            `json:"updated_at"`
	LastActivityAt          int64            `json:"last_activity_at,omitempty"`
	LastActivityKind        string           `json:"last_activity_kind,omitempty"`
	ConfirmationTokens      map[int64]string `json:"confirmation_tokens,omitempty"`
}
type UpsertInput struct {
	SubscriptionStatus string `json:"subscription_status,omitempty"`
	// Imports suppress both email notifications and outbound webhooks.
	SuppressNotifications bool           `json:"-"`
	BrandID               int64          `json:"brand_id"`
	Email                 string         `json:"email"`
	Name                  string         `json:"name"`
	Fields                map[string]any `json:"fields"`
	Status                string         `json:"status,omitempty"`
	ListIDs               []int64        `json:"list_ids,omitempty"`
	ConsentAt             *int64         `json:"gdpr_consent_at,omitempty"`
	Consent               bool           `json:"consent"`
	ConsentIP             string         `json:"consent_ip"`
	Resubscribe           bool           `json:"resubscribe"`
}
type Suppression struct {
	ID      int64  `json:"id"`
	BrandID *int64 `json:"brand_id"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}
type CustomField struct {
	ID         int64  `json:"id"`
	BrandID    int64  `json:"brand_id"`
	Key        string `json:"key"`
	Label      string `json:"label"`
	Type       string `json:"type"`
	Filterable bool   `json:"is_filterable"`
}
type Segment struct {
	ID      int64  `json:"id"`
	BrandID int64  `json:"brand_id"`
	Name    string `json:"name"`
	Rules   Rule   `json:"rules"`
}
type Activity struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	At     int64  `json:"at"`
	Detail string `json:"detail"`
}

func nowMS() int64 { return time.Now().UnixMilli() }

// SetBaseURL configures confirmation links before the service is used.
func (s *Service) SetBaseURL(url string) { s.baseURL = url }
