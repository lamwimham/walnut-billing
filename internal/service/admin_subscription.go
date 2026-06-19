package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var ErrInvalidAdminSubscriptionQuery = errors.New("invalid admin subscription query")

const (
	defaultAdminSubscriptionLimit = 50
	maxAdminSubscriptionLimit     = 100
)

type AdminSubscriptionService interface {
	ListSubscriptions(ctx context.Context, query AdminSubscriptionQuery) (*AdminSubscriptionList, error)
}

type AdminSubscriptionQuery struct {
	UserID     string
	SKUCode    string
	Status     string
	Provider   string
	OutTradeNo string
	Limit      int
	Offset     int
}

type AdminSubscriptionDependencies struct {
	ReadModel repository.AdminSubscriptionReadRepository
	Privacy   AdminPrivacyProjector
	Now       func() time.Time
}

type AdminSubscriptionList struct {
	GeneratedAt   string                    `json:"generated_at"`
	Total         int64                     `json:"total"`
	Subscriptions []AdminSubscriptionRecord `json:"subscriptions"`
}

type AdminSubscriptionRecord struct {
	User                  AdminSubscriptionUser          `json:"user"`
	SKUCode               string                         `json:"sku_code"`
	Status                string                         `json:"status"`
	CancelAtPeriodEnd     bool                           `json:"cancel_at_period_end"`
	CurrentPeriodStartAt  string                         `json:"current_period_start_at,omitempty"`
	CurrentPeriodEndsAt   string                         `json:"current_period_ends_at,omitempty"`
	CanCancel             bool                           `json:"can_cancel"`
	CanResume             bool                           `json:"can_resume"`
	ActiveEntitlements    []string                       `json:"active_entitlements"`
	FulfillmentGrantCount int                            `json:"fulfillment_grant_count"`
	LatestGrantStartsAt   string                         `json:"latest_grant_starts_at,omitempty"`
	LatestGrantExpiresAt  string                         `json:"latest_grant_expires_at,omitempty"`
	LatestOrder           AdminSubscriptionOrder         `json:"latest_order,omitempty"`
	Cancellation          AdminSubscriptionCancellation  `json:"cancellation,omitempty"`
	ProviderControl       AdminSubscriptionProviderState `json:"provider_control,omitempty"`
	PaymentEvents         AdminSubscriptionPaymentEvents `json:"payment_events"`
}

type AdminSubscriptionUser struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	EmailMasked      string `json:"email_masked"`
	EmailFingerprint string `json:"email_fingerprint"`
	EmailDomain      string `json:"email_domain"`
}

type AdminSubscriptionOrder struct {
	OutTradeNo              string `json:"out_trade_no,omitempty"`
	Status                  string `json:"status,omitempty"`
	Provider                string `json:"provider,omitempty"`
	OrderType               string `json:"order_type,omitempty"`
	PaidAt                  string `json:"paid_at,omitempty"`
	FulfilledAt             string `json:"fulfilled_at,omitempty"`
	HasProviderSubscription bool   `json:"has_provider_subscription"`
	HasMetadata             bool   `json:"has_metadata"`
}

type AdminSubscriptionCancellation struct {
	ID                  string `json:"id,omitempty"`
	Status              string `json:"status,omitempty"`
	CancelAtPeriodEnd   bool   `json:"cancel_at_period_end"`
	CurrentPeriodEndsAt string `json:"current_period_ends_at,omitempty"`
	SourceOrderNo       string `json:"source_order_no,omitempty"`
	ReasonRedacted      string `json:"reason_redacted,omitempty"`
	Source              string `json:"source,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	ResumedAt           string `json:"resumed_at,omitempty"`
}

type AdminSubscriptionProviderState struct {
	Status               string `json:"status,omitempty"`
	RawStatus            string `json:"raw_status,omitempty"`
	CurrentPeriodStartAt string `json:"current_period_start_at,omitempty"`
	CurrentPeriodEndsAt  string `json:"current_period_ends_at,omitempty"`
}

type AdminSubscriptionPaymentEvents struct {
	Count       int                           `json:"count"`
	LatestEvent AdminSubscriptionPaymentEvent `json:"latest_event,omitempty"`
}

type AdminSubscriptionPaymentEvent struct {
	ID          string `json:"id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	EventType   string `json:"event_type,omitempty"`
	Status      string `json:"status,omitempty"`
	PayloadHash string `json:"payload_hash,omitempty"`
	ReceivedAt  string `json:"received_at,omitempty"`
	ProcessedAt string `json:"processed_at,omitempty"`
}

type adminSubscriptionService struct {
	readModel repository.AdminSubscriptionReadRepository
	privacy   AdminPrivacyProjector
	now       func() time.Time
}

func NewAdminSubscriptionService(deps AdminSubscriptionDependencies) AdminSubscriptionService {
	privacy := deps.Privacy
	if privacy == (AdminPrivacyProjector{}) {
		privacy = NewAdminPrivacyProjector()
	}
	return &adminSubscriptionService{
		readModel: deps.ReadModel,
		privacy:   privacy,
		now:       deps.Now,
	}
}

func (s *adminSubscriptionService) ListSubscriptions(ctx context.Context, query AdminSubscriptionQuery) (*AdminSubscriptionList, error) {
	if s == nil || s.readModel == nil {
		return nil, ErrInvalidAdminSubscriptionQuery
	}
	repoQuery := repository.AdminSubscriptionQuery{
		UserID:     strings.TrimSpace(query.UserID),
		SKUCode:    strings.TrimSpace(query.SKUCode),
		Status:     strings.TrimSpace(query.Status),
		Provider:   strings.TrimSpace(query.Provider),
		OutTradeNo: strings.TrimSpace(query.OutTradeNo),
		Limit:      normalizeAdminSubscriptionQueryLimit(query.Limit),
		Offset:     maxInt(query.Offset, 0),
	}
	readModel, err := s.readModel.List(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	if readModel == nil {
		return nil, ErrInvalidAdminSubscriptionQuery
	}
	now := s.currentTime()
	records := make([]AdminSubscriptionRecord, 0, len(readModel.Records))
	for _, record := range readModel.Records {
		projected, err := s.projectSubscription(ctx, record, now)
		if err != nil {
			return nil, err
		}
		if record.SKUCode != "" && projected.SKUCode != "" && projected.SKUCode != record.SKUCode {
			continue
		}
		if repoQuery.SKUCode != "" && projected.SKUCode != repoQuery.SKUCode {
			continue
		}
		if repoQuery.Status != "" && projected.Status != repoQuery.Status {
			continue
		}
		records = append(records, projected)
	}
	total := int64(len(records))
	records = sliceAdminSubscriptionRecords(records, repoQuery.Limit, repoQuery.Offset)
	return &AdminSubscriptionList{
		GeneratedAt:   formatTime(now),
		Total:         total,
		Subscriptions: records,
	}, nil
}

func (s *adminSubscriptionService) projectSubscription(ctx context.Context, record repository.AdminSubscriptionRecord, now time.Time) (AdminSubscriptionRecord, error) {
	projectionGrants := adminSubscriptionGrantsForSKU(record.Grants, record.SKUCode)
	projection, err := projectSoftwareSubscriptionFromGrants(ctx, adminSubscriptionCancellationLookup{cancellation: record.LatestCancellation, now: now}, record.User.ID, projectionGrants, now)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return AdminSubscriptionRecord{}, ErrInvalidAdminSubscriptionQuery
		}
		return AdminSubscriptionRecord{}, err
	}
	if projection.SKUCode == "" {
		projection.SKUCode = record.SKUCode
	}
	projection.Status = defaultString(adminSubscriptionFallbackStatus(record, projection, projectionGrants, now), SoftwareSubscriptionStatusNone)
	activeEntitlements, latestStartsAt, latestExpiresAt := projectAdminSubscriptionGrants(projectionGrants, now)
	return AdminSubscriptionRecord{
		User:                  s.projectUser(record.User),
		SKUCode:               defaultString(projection.SKUCode, record.SKUCode),
		Status:                defaultString(projection.Status, SoftwareSubscriptionStatusNone),
		CancelAtPeriodEnd:     projection.CancelAtPeriodEnd,
		CurrentPeriodStartAt:  projection.CurrentPeriodStartAt,
		CurrentPeriodEndsAt:   projection.CurrentPeriodEndsAt,
		CanCancel:             softwareSubscriptionIsMonthlyActive(projection) && !softwareSubscriptionIsCancelAtPeriodEnd(projection),
		CanResume:             softwareSubscriptionIsCancelAtPeriodEnd(projection),
		ActiveEntitlements:    activeEntitlements,
		FulfillmentGrantCount: countFulfillmentSubscriptionGrants(projectionGrants),
		LatestGrantStartsAt:   formatTime(latestStartsAt),
		LatestGrantExpiresAt:  formatOptionalTime(latestExpiresAt),
		LatestOrder:           projectAdminSubscriptionOrder(record.LatestOrder, record.PaymentEvents),
		Cancellation:          s.projectCancellation(record.LatestCancellation),
		ProviderControl:       projectAdminSubscriptionProviderState(record.LatestOrder),
		PaymentEvents:         projectAdminSubscriptionPaymentEvents(record.PaymentEvents),
	}, nil
}

func (s *adminSubscriptionService) projectUser(user domain.User) AdminSubscriptionUser {
	email := s.privacy.ProjectEmail(user.Email)
	return AdminSubscriptionUser{
		ID:               user.ID,
		Status:           defaultString(strings.TrimSpace(user.Status), domain.UserStatusActive),
		EmailMasked:      email.Masked,
		EmailFingerprint: email.Fingerprint,
		EmailDomain:      email.Domain,
	}
}

func (s *adminSubscriptionService) projectCancellation(cancellation *domain.SubscriptionCancellation) AdminSubscriptionCancellation {
	if cancellation == nil {
		return AdminSubscriptionCancellation{}
	}
	return AdminSubscriptionCancellation{
		ID:                  cancellation.ID,
		Status:              cancellation.Status,
		CancelAtPeriodEnd:   cancellation.CancelAtPeriodEnd,
		CurrentPeriodEndsAt: formatTime(cancellation.CurrentPeriodEndsAt),
		SourceOrderNo:       cancellation.SourceOrderNo,
		ReasonRedacted:      s.privacy.RedactFreeText(cancellation.Reason),
		Source:              cancellation.Source,
		CreatedAt:           formatTime(cancellation.CreatedAt),
		UpdatedAt:           formatTime(cancellation.UpdatedAt),
		ResumedAt:           formatOptionalTime(cancellation.ResumedAt),
	}
}

func projectAdminSubscriptionOrder(order *domain.Order, events []domain.PaymentEventInbox) AdminSubscriptionOrder {
	if order == nil {
		return AdminSubscriptionOrder{}
	}
	return AdminSubscriptionOrder{
		OutTradeNo:              order.OutTradeNo,
		Status:                  order.Status,
		Provider:                order.Provider,
		OrderType:               defaultString(order.OrderType, domain.OrderTypeCheckout),
		PaidAt:                  formatOptionalTime(order.PaidAt),
		FulfilledAt:             formatOptionalTime(order.FulfilledAt),
		HasProviderSubscription: adminSubscriptionHasProviderSubscription(order, events),
		HasMetadata:             strings.TrimSpace(order.Metadata) != "",
	}
}

func projectAdminSubscriptionProviderState(order *domain.Order) AdminSubscriptionProviderState {
	if order == nil {
		return AdminSubscriptionProviderState{}
	}
	metadata := orderMetadataMap(order.Metadata)
	return AdminSubscriptionProviderState{
		Status:               metadata["walnut_provider_subscription_status"],
		RawStatus:            metadata["walnut_provider_subscription_raw_status"],
		CurrentPeriodStartAt: metadata["walnut_provider_period_start_at"],
		CurrentPeriodEndsAt:  metadata["walnut_provider_period_end_at"],
	}
}

func projectAdminSubscriptionPaymentEvents(events []domain.PaymentEventInbox) AdminSubscriptionPaymentEvents {
	summary := AdminSubscriptionPaymentEvents{Count: len(events)}
	if len(events) == 0 {
		return summary
	}
	event := events[0]
	summary.LatestEvent = AdminSubscriptionPaymentEvent{
		ID:          event.ID,
		Provider:    event.Provider,
		EventType:   event.EventType,
		Status:      event.Status,
		PayloadHash: event.PayloadHash,
		ReceivedAt:  formatTime(event.ReceivedAt),
		ProcessedAt: formatOptionalTime(event.ProcessedAt),
	}
	return summary
}

func projectAdminSubscriptionGrants(grants []domain.EntitlementGrant, now time.Time) ([]string, time.Time, *time.Time) {
	active := map[string]bool{}
	var latestStartsAt time.Time
	var latestExpiresAt *time.Time
	for _, grant := range grants {
		if !adminSubscriptionGrant(grant) {
			continue
		}
		if grant.StartsAt.After(latestStartsAt) {
			latestStartsAt = grant.StartsAt
		}
		if grant.ExpiresAt != nil && (latestExpiresAt == nil || grant.ExpiresAt.After(*latestExpiresAt)) {
			copy := grant.ExpiresAt.UTC()
			latestExpiresAt = &copy
		}
		if grantIsActive(grant, now) {
			active[grant.EntitlementID] = true
		}
	}
	return sortedKeys(active), latestStartsAt, latestExpiresAt
}

func countFulfillmentSubscriptionGrants(grants []domain.EntitlementGrant) int {
	total := 0
	for _, grant := range grants {
		if adminSubscriptionGrant(grant) {
			total++
		}
	}
	return total
}

func adminSubscriptionGrant(grant domain.EntitlementGrant) bool {
	return grant.Source == domain.GrantSourceFulfillment && IsCurrentAdvancedEntitlementID(grant.EntitlementID)
}

func adminSubscriptionGrantsForSKU(grants []domain.EntitlementGrant, skuCode string) []domain.EntitlementGrant {
	skuCode = strings.TrimSpace(skuCode)
	if skuCode == "" {
		return grants
	}
	filtered := make([]domain.EntitlementGrant, 0, len(grants))
	for _, grant := range grants {
		if !adminSubscriptionGrant(grant) {
			continue
		}
		switch skuCode {
		case domain.SKUProOwnAIMonthly:
			if grant.ExpiresAt != nil {
				filtered = append(filtered, grant)
			}
		case domain.SKUProOwnAILifetime:
			if grant.ExpiresAt == nil {
				filtered = append(filtered, grant)
			}
		default:
			filtered = append(filtered, grant)
		}
	}
	return filtered
}

func adminSubscriptionHasProviderSubscription(order *domain.Order, events []domain.PaymentEventInbox) bool {
	if order == nil {
		return false
	}
	metadata := orderMetadataMap(order.Metadata)
	if metadata["walnut_provider_subscription_id"] != "" || metadata["provider_subscription_id"] != "" {
		return true
	}
	for _, event := range events {
		if providerSubscriptionIDFromRawPayload(event.RawPayload) != "" {
			return true
		}
	}
	return false
}

func adminSubscriptionFallbackStatus(record repository.AdminSubscriptionRecord, projection SoftwareSubscriptionProjection, grants []domain.EntitlementGrant, now time.Time) string {
	if projection.Status != "" && projection.Status != SoftwareSubscriptionStatusNone {
		return projection.Status
	}
	if record.LatestOrder != nil {
		switch record.LatestOrder.Status {
		case domain.OrderStatusCancelled, domain.OrderStatusRefunded:
			return SoftwareSubscriptionStatusCancelled
		}
	}
	if hasExpiredAdminSubscriptionGrant(grants, now) {
		return SoftwareSubscriptionStatusExpired
	}
	return projection.Status
}

func hasExpiredAdminSubscriptionGrant(grants []domain.EntitlementGrant, now time.Time) bool {
	for _, grant := range grants {
		if !adminSubscriptionGrant(grant) || grant.ExpiresAt == nil {
			continue
		}
		if !grant.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (s *adminSubscriptionService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func normalizeAdminSubscriptionQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminSubscriptionLimit
	}
	if limit > maxAdminSubscriptionLimit {
		return maxAdminSubscriptionLimit
	}
	return limit
}

func sliceAdminSubscriptionRecords(records []AdminSubscriptionRecord, limit int, offset int) []AdminSubscriptionRecord {
	if offset >= len(records) {
		return []AdminSubscriptionRecord{}
	}
	end := offset + limit
	if end > len(records) {
		end = len(records)
	}
	return records[offset:end]
}

type adminSubscriptionCancellationLookup struct {
	cancellation *domain.SubscriptionCancellation
	now          time.Time
}

func (l adminSubscriptionCancellationLookup) Create(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	return ErrInvalidAdminSubscriptionQuery
}

func (l adminSubscriptionCancellationLookup) GetByIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	return nil, repository.ErrNotFound
}

func (l adminSubscriptionCancellationLookup) GetByResumeIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	return nil, repository.ErrNotFound
}

func (l adminSubscriptionCancellationLookup) FindActive(ctx context.Context, query repository.SubscriptionCancellationQuery) (*domain.SubscriptionCancellation, error) {
	if l.cancellation == nil {
		return nil, repository.ErrNotFound
	}
	if query.UserID != "" && l.cancellation.UserID != query.UserID {
		return nil, repository.ErrNotFound
	}
	if query.SKUCode != "" && l.cancellation.SKUCode != query.SKUCode {
		return nil, repository.ErrNotFound
	}
	if query.Status != "" && l.cancellation.Status != query.Status {
		return nil, repository.ErrNotFound
	}
	now := l.now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !l.cancellation.CancelAtPeriodEnd || !l.cancellation.CurrentPeriodEndsAt.After(now) {
		return nil, repository.ErrNotFound
	}
	return l.cancellation, nil
}

func (l adminSubscriptionCancellationLookup) Update(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	return ErrInvalidAdminSubscriptionQuery
}
