package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var ErrInvalidAdminUserAccessSummary = errors.New("invalid admin user access summary request")

const (
	defaultAdminUserAccessSummaryLimit = 10
	maxAdminUserAccessSummaryLimit     = 50
)

type AdminUserAccessSummaryService interface {
	Get(ctx context.Context, input AdminUserAccessSummaryInput) (*AdminUserAccessSummary, error)
}

type AdminUserAccessSummaryInput struct {
	UserID      string
	RecentLimit int
}

type AdminUserAccessSummaryDependencies struct {
	ReadModel             repository.AdminUserAccessSummaryReadRepository
	SoftwareSubscriptions SoftwareSubscriptionProjector
	CloudQuotaPolicy      CloudStorageQuotaPolicy
	Privacy               AdminPrivacyProjector
	MaxDevices            int
	Now                   func() time.Time
}

type AdminUserAccessSummary struct {
	GeneratedAt   string                              `json:"generated_at"`
	User          AdminUserAccessIdentity             `json:"user"`
	Devices       AdminUserAccessDeviceSummary        `json:"devices"`
	Trial         AdminUserAccessTrialSummary         `json:"trial"`
	Grants        AdminUserAccessGrantSummary         `json:"grants"`
	Subscription  SoftwareSubscriptionProjection      `json:"subscription"`
	Orders        []AdminUserAccessOrderRecord        `json:"orders"`
	PaymentEvents []AdminUserAccessPaymentEventRecord `json:"payment_events"`
	RiskFlags     AdminUserAccessRiskSummary          `json:"risk_flags"`
	CloudStorage  AdminUserAccessCloudSummary         `json:"cloud_storage"`
}

type AdminUserAccessIdentity struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	EmailMasked       string `json:"email_masked"`
	EmailFingerprint  string `json:"email_fingerprint"`
	EmailDomain       string `json:"email_domain"`
	DisplayNameMasked string `json:"display_name_masked"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type AdminUserAccessDeviceSummary struct {
	TotalCount       int                  `json:"total_count"`
	ActiveCount      int                  `json:"active_count"`
	RevokedCount     int                  `json:"revoked_count"`
	Capacity         AccessDeviceCapacity `json:"capacity"`
	LastSeenAt       string               `json:"last_seen_at"`
	RecentDeviceRows []AccessDeviceRecord `json:"recent_device_rows"`
}

type AdminUserAccessTrialSummary struct {
	CurrentStatus string                       `json:"current_status"`
	Latest        AdminUserAccessTrialRecord   `json:"latest"`
	Recent        []AdminUserAccessTrialRecord `json:"recent"`
}

type AdminUserAccessTrialRecord struct {
	ID        string `json:"id"`
	GrantType string `json:"grant_type"`
	Status    string `json:"status"`
	StartsAt  string `json:"starts_at"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
	RevokedAt string `json:"revoked_at,omitempty"`
}

type AdminUserAccessGrantSummary struct {
	CurrentEntitlements []string                     `json:"current_entitlements"`
	LegacyEntitlements  []string                     `json:"legacy_entitlements"`
	Active              []AdminUserAccessGrantRecord `json:"active"`
	Revoked             []AdminUserAccessGrantRecord `json:"revoked"`
	Expired             []AdminUserAccessGrantRecord `json:"expired"`
}

type AdminUserAccessGrantRecord struct {
	ID            string `json:"id"`
	EntitlementID string `json:"entitlement_id"`
	Status        string `json:"status"`
	Source        string `json:"source"`
	StartsAt      string `json:"starts_at"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	RevokedAt     string `json:"revoked_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type AdminUserAccessOrderRecord struct {
	OutTradeNo  string `json:"out_trade_no"`
	SKUCode     string `json:"sku_code"`
	Status      string `json:"status"`
	Provider    string `json:"provider"`
	OrderType   string `json:"order_type"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	PaidAt      string `json:"paid_at,omitempty"`
	FulfilledAt string `json:"fulfilled_at,omitempty"`
	HasCheckout bool   `json:"has_checkout"`
	HasMetadata bool   `json:"has_metadata"`
}

type AdminUserAccessPaymentEventRecord struct {
	ID                string `json:"id"`
	Provider          string `json:"provider"`
	EventType         string `json:"event_type"`
	OutTradeNo        string `json:"out_trade_no"`
	Amount            int64  `json:"amount"`
	Currency          string `json:"currency"`
	PayloadHash       string `json:"payload_hash"`
	Status            string `json:"status"`
	Attempts          int    `json:"attempts"`
	LastErrorRedacted string `json:"last_error_redacted,omitempty"`
	ReceivedAt        string `json:"received_at"`
	ProcessedAt       string `json:"processed_at,omitempty"`
}

type AdminUserAccessRiskSummary struct {
	OpenCount         int                             `json:"open_count"`
	ResolvedCount     int                             `json:"resolved_count"`
	CriticalOpenCount int                             `json:"critical_open_count"`
	Recent            []AdminUserAccessRiskFlagRecord `json:"recent"`
}

type AdminUserAccessRiskFlagRecord struct {
	ID           string               `json:"id"`
	OutTradeNo   string               `json:"out_trade_no"`
	Reason       string               `json:"reason"`
	Severity     string               `json:"severity"`
	Status       string               `json:"status"`
	NoteRedacted string               `json:"note_redacted,omitempty"`
	ResolvedBy   AdminActorProjection `json:"resolved_by,omitempty"`
	CreatedAt    string               `json:"created_at"`
	ResolvedAt   string               `json:"resolved_at,omitempty"`
}

type AdminUserAccessCloudSummary struct {
	Plan               string                        `json:"plan"`
	UsedBytes          int64                         `json:"used_bytes"`
	QuotaBytes         int64                         `json:"quota_bytes"`
	RemainingBytes     int64                         `json:"remaining_bytes"`
	OverQuota          bool                          `json:"over_quota"`
	ProjectCount       int                           `json:"project_count"`
	ActiveProjectCount int                           `json:"active_project_count"`
	RecentProjects     []AdminUserAccessCloudProject `json:"recent_projects"`
}

type AdminUserAccessCloudProject struct {
	ID              string `json:"id"`
	ClientProjectID string `json:"client_project_id"`
	NameMasked      string `json:"name_masked"`
	Status          string `json:"status"`
	LastManifestID  string `json:"last_manifest_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type adminUserAccessSummaryService struct {
	readModel             repository.AdminUserAccessSummaryReadRepository
	softwareSubscriptions SoftwareSubscriptionProjector
	cloudQuotaPolicy      CloudStorageQuotaPolicy
	privacy               AdminPrivacyProjector
	maxDevices            int
	now                   func() time.Time
}

func NewAdminUserAccessSummaryService(deps AdminUserAccessSummaryDependencies) AdminUserAccessSummaryService {
	privacy := deps.Privacy
	if privacy == (AdminPrivacyProjector{}) {
		privacy = NewAdminPrivacyProjector()
	}
	return &adminUserAccessSummaryService{
		readModel:             deps.ReadModel,
		softwareSubscriptions: deps.SoftwareSubscriptions,
		cloudQuotaPolicy:      deps.CloudQuotaPolicy,
		privacy:               privacy,
		maxDevices:            deps.MaxDevices,
		now:                   deps.Now,
	}
}

func (s *adminUserAccessSummaryService) Get(ctx context.Context, input AdminUserAccessSummaryInput) (*AdminUserAccessSummary, error) {
	userID := strings.TrimSpace(input.UserID)
	if s == nil || s.readModel == nil || userID == "" {
		return nil, ErrInvalidAdminUserAccessSummary
	}
	limit := normalizeAdminUserAccessSummaryLimit(input.RecentLimit)
	record, err := s.readModel.Get(ctx, repository.AdminUserAccessSummaryQuery{UserID: userID, RecentLimit: limit})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if record == nil || strings.TrimSpace(record.User.ID) == "" {
		return nil, ErrUserNotFound
	}
	now := s.currentTime()
	subscription, err := s.projectSubscription(ctx, record.User.ID)
	if err != nil {
		return nil, err
	}
	return &AdminUserAccessSummary{
		GeneratedAt:   formatTime(now),
		User:          s.projectIdentity(record.User),
		Devices:       projectAdminUserAccessDevices(record.Devices, s.maxDevices, limit),
		Trial:         projectAdminUserAccessTrials(record.TrialGrants, now, limit),
		Grants:        projectAdminUserAccessGrants(record.EntitlementGrants, now, limit),
		Subscription:  subscription,
		Orders:        projectAdminUserAccessOrders(record.Orders, limit),
		PaymentEvents: s.projectPaymentEvents(record.PaymentEvents, limit),
		RiskFlags:     s.projectRiskFlags(record.RiskFlags, limit),
		CloudStorage:  s.projectCloudStorage(ctx, record, limit),
	}, nil
}

func (s *adminUserAccessSummaryService) projectSubscription(ctx context.Context, userID string) (SoftwareSubscriptionProjection, error) {
	if s == nil || s.softwareSubscriptions == nil {
		return SoftwareSubscriptionProjection{UserID: userID, Status: SoftwareSubscriptionStatusNone}, nil
	}
	projection, err := s.softwareSubscriptions.Project(ctx, userID)
	if err != nil {
		return SoftwareSubscriptionProjection{}, err
	}
	if projection.UserID == "" {
		projection.UserID = userID
	}
	if projection.Status == "" {
		projection.Status = SoftwareSubscriptionStatusNone
	}
	return projection, nil
}

func (s *adminUserAccessSummaryService) projectIdentity(user domain.User) AdminUserAccessIdentity {
	email := s.privacy.ProjectEmail(user.Email)
	return AdminUserAccessIdentity{
		ID:                user.ID,
		Status:            defaultString(strings.TrimSpace(user.Status), domain.UserStatusActive),
		EmailMasked:       email.Masked,
		EmailFingerprint:  email.Fingerprint,
		EmailDomain:       email.Domain,
		DisplayNameMasked: maskDisplayName(user.DisplayName),
		CreatedAt:         formatTime(user.CreatedAt),
		UpdatedAt:         formatTime(user.UpdatedAt),
	}
}

func (s *adminUserAccessSummaryService) projectPaymentEvents(events []domain.PaymentEventInbox, limit int) []AdminUserAccessPaymentEventRecord {
	events = limitPaymentEvents(events, limit)
	result := make([]AdminUserAccessPaymentEventRecord, 0, len(events))
	for _, event := range events {
		result = append(result, AdminUserAccessPaymentEventRecord{
			ID:                event.ID,
			Provider:          event.Provider,
			EventType:         event.EventType,
			OutTradeNo:        event.OutTradeNo,
			Amount:            event.Amount,
			Currency:          event.Currency,
			PayloadHash:       event.PayloadHash,
			Status:            event.Status,
			Attempts:          event.Attempts,
			LastErrorRedacted: s.privacy.RedactFreeText(event.LastError),
			ReceivedAt:        formatTime(event.ReceivedAt),
			ProcessedAt:       formatOptionalTime(event.ProcessedAt),
		})
	}
	return result
}

func (s *adminUserAccessSummaryService) projectRiskFlags(flags []domain.PaymentRiskFlag, limit int) AdminUserAccessRiskSummary {
	summary := AdminUserAccessRiskSummary{Recent: make([]AdminUserAccessRiskFlagRecord, 0, minInt(len(flags), limit))}
	for _, flag := range flags {
		switch flag.Status {
		case domain.PaymentRiskStatusResolved:
			summary.ResolvedCount++
		default:
			summary.OpenCount++
			if flag.Severity == domain.PaymentRiskSeverityCritical {
				summary.CriticalOpenCount++
			}
		}
	}
	for _, flag := range limitRiskFlags(flags, limit) {
		summary.Recent = append(summary.Recent, AdminUserAccessRiskFlagRecord{
			ID:           flag.ID,
			OutTradeNo:   flag.OutTradeNo,
			Reason:       flag.Reason,
			Severity:     flag.Severity,
			Status:       flag.Status,
			NoteRedacted: s.privacy.RedactFreeText(flag.Note),
			ResolvedBy:   s.privacy.ProjectActor(flag.ResolvedBy),
			CreatedAt:    formatTime(flag.CreatedAt),
			ResolvedAt:   formatOptionalTime(flag.ResolvedAt),
		})
	}
	return summary
}

func (s *adminUserAccessSummaryService) projectCloudStorage(ctx context.Context, record *repository.AdminUserAccessSummaryRecord, limit int) AdminUserAccessCloudSummary {
	if record == nil {
		return AdminUserAccessCloudSummary{}
	}
	now := s.currentTime()
	quota := CloudStorageQuotaDecision{Plan: cloudStoragePlanForGrants(record.EntitlementGrants, now)}
	if s.cloudQuotaPolicy != nil {
		quota = s.cloudQuotaPolicy.Decide(ctx, CloudStorageQuotaInput{User: &record.User, Grants: record.EntitlementGrants, Now: now})
	}
	usage := usageFor(record.User.ID, quota.Plan, record.CloudUsedBytes, quota.QuotaBytes)
	summary := AdminUserAccessCloudSummary{
		Plan:           usage.Plan,
		UsedBytes:      usage.UsedBytes,
		QuotaBytes:     usage.QuotaBytes,
		RemainingBytes: usage.RemainingBytes,
		OverQuota:      usage.OverQuota,
		ProjectCount:   len(record.CloudProjects),
		RecentProjects: make([]AdminUserAccessCloudProject, 0, minInt(len(record.CloudProjects), limit)),
	}
	for _, project := range record.CloudProjects {
		if project.Status == "" || project.Status == domain.CloudProjectStatusActive {
			summary.ActiveProjectCount++
		}
	}
	for _, project := range limitCloudProjects(record.CloudProjects, limit) {
		summary.RecentProjects = append(summary.RecentProjects, AdminUserAccessCloudProject{
			ID:              project.ID,
			ClientProjectID: project.ClientProjectID,
			NameMasked:      maskToken(project.Name),
			Status:          defaultString(project.Status, domain.CloudProjectStatusActive),
			LastManifestID:  project.LastManifestID,
			CreatedAt:       formatTime(project.CreatedAt),
			UpdatedAt:       formatTime(project.UpdatedAt),
		})
	}
	return summary
}

func (s *adminUserAccessSummaryService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func projectAdminUserAccessDevices(devices []domain.UserDevice, maxDevices int, limit int) AdminUserAccessDeviceSummary {
	summary := AdminUserAccessDeviceSummary{
		TotalCount:       len(devices),
		ActiveCount:      countActiveDevices(devices),
		LastSeenAt:       formatTime(latestDeviceSeenAt(devices)),
		RecentDeviceRows: limitAccessDeviceRecords(projectAccessDevices(devices), limit),
	}
	for _, device := range devices {
		if device.Status == domain.DeviceStatusDisabled || device.RevokedAt != nil {
			summary.RevokedCount++
		}
	}
	summary.Capacity = newAccessDeviceCapacity(summary.ActiveCount, maxDevices)
	return summary
}

func projectAdminUserAccessTrials(trials []domain.TrialGrant, now time.Time, limit int) AdminUserAccessTrialSummary {
	latest := latestTrialGrant(trials)
	summary := AdminUserAccessTrialSummary{
		CurrentStatus: trialStatus(latest, now),
		Latest:        projectAdminUserAccessTrial(latest, now),
		Recent:        make([]AdminUserAccessTrialRecord, 0, minInt(len(trials), limit)),
	}
	for _, trial := range limitTrialGrants(trials, limit) {
		summary.Recent = append(summary.Recent, projectAdminUserAccessTrial(trial, now))
	}
	return summary
}

func projectAdminUserAccessTrial(trial domain.TrialGrant, now time.Time) AdminUserAccessTrialRecord {
	if trial.ID == "" {
		return AdminUserAccessTrialRecord{Status: "none"}
	}
	return AdminUserAccessTrialRecord{
		ID:        trial.ID,
		GrantType: trial.GrantType,
		Status:    trialStatus(trial, now),
		StartsAt:  formatTime(trial.StartsAt),
		ExpiresAt: formatOptionalTime(trial.ExpiresAt),
		CreatedAt: formatTime(trial.CreatedAt),
		RevokedAt: formatOptionalTime(trial.RevokedAt),
	}
}

func projectAdminUserAccessGrants(grants []domain.EntitlementGrant, now time.Time, limit int) AdminUserAccessGrantSummary {
	current, legacy := splitCurrentAndLegacyEntitlements(grants, now)
	summary := AdminUserAccessGrantSummary{
		CurrentEntitlements: current,
		LegacyEntitlements:  legacy,
		Active:              []AdminUserAccessGrantRecord{},
		Revoked:             []AdminUserAccessGrantRecord{},
		Expired:             []AdminUserAccessGrantRecord{},
	}
	for _, grant := range grants {
		record := projectAdminUserAccessGrant(grant)
		switch {
		case grant.Status == domain.GrantStatusRevoked || grant.RevokedAt != nil:
			summary.Revoked = append(summary.Revoked, record)
		case grantIsActive(grant, now):
			summary.Active = append(summary.Active, record)
		default:
			summary.Expired = append(summary.Expired, record)
		}
	}
	summary.Active = limitGrantRecords(summary.Active, limit)
	summary.Revoked = limitGrantRecords(summary.Revoked, limit)
	summary.Expired = limitGrantRecords(summary.Expired, limit)
	return summary
}

func projectAdminUserAccessGrant(grant domain.EntitlementGrant) AdminUserAccessGrantRecord {
	return AdminUserAccessGrantRecord{
		ID:            grant.ID,
		EntitlementID: grant.EntitlementID,
		Status:        defaultString(grant.Status, domain.GrantStatusActive),
		Source:        grant.Source,
		StartsAt:      formatTime(grant.StartsAt),
		ExpiresAt:     formatOptionalTime(grant.ExpiresAt),
		RevokedAt:     formatOptionalTime(grant.RevokedAt),
		CreatedAt:     formatTime(grant.CreatedAt),
	}
}

func projectAdminUserAccessOrders(orders []domain.Order, limit int) []AdminUserAccessOrderRecord {
	orders = limitOrders(orders, limit)
	result := make([]AdminUserAccessOrderRecord, 0, len(orders))
	for _, order := range orders {
		result = append(result, AdminUserAccessOrderRecord{
			OutTradeNo:  order.OutTradeNo,
			SKUCode:     order.SKUCode,
			Status:      order.Status,
			Provider:    order.Provider,
			OrderType:   order.OrderType,
			Amount:      order.Amount,
			Currency:    order.Currency,
			PaidAt:      formatOptionalTime(order.PaidAt),
			FulfilledAt: formatOptionalTime(order.FulfilledAt),
			HasCheckout: strings.TrimSpace(order.CheckoutURL) != "" || strings.TrimSpace(order.ProviderCheckoutID) != "",
			HasMetadata: strings.TrimSpace(order.Metadata) != "",
		})
	}
	return result
}

func normalizeAdminUserAccessSummaryLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminUserAccessSummaryLimit
	}
	if limit > maxAdminUserAccessSummaryLimit {
		return maxAdminUserAccessSummaryLimit
	}
	return limit
}

func hasActiveEntitlement(grants []domain.EntitlementGrant, entitlementID string, now time.Time) bool {
	for _, grant := range grants {
		if grant.EntitlementID == entitlementID && grantIsActive(grant, now) {
			return true
		}
	}
	return false
}

func limitAccessDeviceRecords(records []AccessDeviceRecord, limit int) []AccessDeviceRecord {
	if limit <= 0 || len(records) <= limit {
		return records
	}
	return records[:limit]
}

func limitTrialGrants(trials []domain.TrialGrant, limit int) []domain.TrialGrant {
	if limit <= 0 || len(trials) <= limit {
		return trials
	}
	return trials[:limit]
}

func limitGrantRecords(records []AdminUserAccessGrantRecord, limit int) []AdminUserAccessGrantRecord {
	if limit <= 0 || len(records) <= limit {
		return records
	}
	return records[:limit]
}

func limitOrders(orders []domain.Order, limit int) []domain.Order {
	if limit <= 0 || len(orders) <= limit {
		return orders
	}
	return orders[:limit]
}

func limitPaymentEvents(events []domain.PaymentEventInbox, limit int) []domain.PaymentEventInbox {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return events[:limit]
}

func limitRiskFlags(flags []domain.PaymentRiskFlag, limit int) []domain.PaymentRiskFlag {
	if limit <= 0 || len(flags) <= limit {
		return flags
	}
	return flags[:limit]
}

func limitCloudProjects(projects []domain.CloudProject, limit int) []domain.CloudProject {
	if limit <= 0 || len(projects) <= limit {
		return projects
	}
	return projects[:limit]
}
