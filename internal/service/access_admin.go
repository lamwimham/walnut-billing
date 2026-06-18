package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

const (
	defaultAccessAccountLimit = 50
	maxAccessAccountLimit     = 100
)

type AccessAdminService interface {
	ListAccounts(ctx context.Context, query AccessAdminQuery) (*AccessAccountList, error)
}

type AccessAdminQuery struct {
	UserID string
	Email  string
	Status string
	Limit  int
	Offset int
}

type AccessAccountList struct {
	Total    int64                 `json:"total"`
	Accounts []AccessAccountRecord `json:"accounts"`
}

type AccessAccountRecord struct {
	UserID              string               `json:"user_id"`
	EmailMasked         string               `json:"email_masked"`
	EmailFingerprint    string               `json:"email_fingerprint"`
	EmailDomain         string               `json:"email_domain"`
	DisplayNameMasked   string               `json:"display_name_masked"`
	Status              string               `json:"status"`
	DeviceCount         int                  `json:"device_count"`
	ActiveDeviceCount   int                  `json:"active_device_count"`
	Devices             []AccessDeviceRecord `json:"devices"`
	LastSeenAt          string               `json:"last_seen_at"`
	TrialGrantType      string               `json:"trial_grant_type"`
	TrialStatus         string               `json:"trial_status"`
	TrialStartsAt       string               `json:"trial_starts_at"`
	TrialExpiresAt      string               `json:"trial_expires_at"`
	CurrentEntitlements []string             `json:"current_entitlements"`
	LegacyEntitlements  []string             `json:"legacy_entitlements"`
	CreatedAt           string               `json:"created_at"`
	UpdatedAt           string               `json:"updated_at"`
}

type AccessDeviceRecord struct {
	ID                  string `json:"id"`
	DeviceIDMasked      string `json:"device_id_masked"`
	DeviceIDFingerprint string `json:"device_id_fingerprint"`
	Status              string `json:"status"`
	FirstSeenAt         string `json:"first_seen_at"`
	LastSeenAt          string `json:"last_seen_at"`
	RevokedAt           string `json:"revoked_at,omitempty"`
}

type accessAdminServiceImpl struct {
	repo repository.AccessAccountReadRepository
	now  func() time.Time
}

func NewAccessAdminService(repo repository.AccessAccountReadRepository) AccessAdminService {
	return &accessAdminServiceImpl{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

func (s *accessAdminServiceImpl) ListAccounts(ctx context.Context, query AccessAdminQuery) (*AccessAccountList, error) {
	if s == nil || s.repo == nil {
		return &AccessAccountList{}, nil
	}
	repoQuery := repository.AccessAccountQuery{
		UserID: strings.TrimSpace(query.UserID),
		Email:  normalizeEmail(query.Email),
		Status: strings.TrimSpace(query.Status),
		Limit:  normalizeAccessAccountLimit(query.Limit),
		Offset: maxInt(query.Offset, 0),
	}
	records, total, err := s.repo.List(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	accounts := make([]AccessAccountRecord, 0, len(records))
	for _, record := range records {
		accounts = append(accounts, projectAccessAccount(record, s.now()))
	}
	return &AccessAccountList{Total: total, Accounts: accounts}, nil
}

func projectAccessAccount(record repository.AccessAccountRecord, now time.Time) AccessAccountRecord {
	currentEntitlements, legacyEntitlements := splitCurrentAndLegacyEntitlements(record.EntitlementGrants, now)
	trial := latestTrialGrant(record.TrialGrants)
	return AccessAccountRecord{
		UserID:              record.User.ID,
		EmailMasked:         maskEmail(record.User.Email),
		EmailFingerprint:    emailFingerprint(record.User.Email),
		EmailDomain:         emailDomain(record.User.Email),
		DisplayNameMasked:   maskDisplayName(record.User.DisplayName),
		Status:              record.User.Status,
		DeviceCount:         len(record.Devices),
		ActiveDeviceCount:   countActiveDevices(record.Devices),
		Devices:             projectAccessDevices(record.Devices),
		LastSeenAt:          formatTime(latestDeviceSeenAt(record.Devices)),
		TrialGrantType:      trial.GrantType,
		TrialStatus:         trialStatus(trial, now),
		TrialStartsAt:       formatTime(trial.StartsAt),
		TrialExpiresAt:      formatOptionalTime(trial.ExpiresAt),
		CurrentEntitlements: currentEntitlements,
		LegacyEntitlements:  legacyEntitlements,
		CreatedAt:           formatTime(record.User.CreatedAt),
		UpdatedAt:           formatTime(record.User.UpdatedAt),
	}
}

func projectAccessDevices(devices []domain.UserDevice) []AccessDeviceRecord {
	result := make([]AccessDeviceRecord, 0, len(devices))
	for _, device := range devices {
		result = append(result, AccessDeviceRecord{
			ID:                  device.ID,
			DeviceIDMasked:      maskToken(device.DeviceID),
			DeviceIDFingerprint: deviceFingerprint(device.DeviceID),
			Status:              defaultString(device.Status, domain.DeviceStatusActive),
			FirstSeenAt:         formatTime(device.FirstSeenAt),
			LastSeenAt:          formatTime(device.LastSeenAt),
			RevokedAt:           formatOptionalTime(device.RevokedAt),
		})
	}
	return result
}

func normalizeAccessAccountLimit(limit int) int {
	if limit <= 0 {
		return defaultAccessAccountLimit
	}
	if limit > maxAccessAccountLimit {
		return maxAccessAccountLimit
	}
	return limit
}

func countActiveDevices(devices []domain.UserDevice) int {
	count := 0
	for _, device := range devices {
		if device.Status == "" || device.Status == domain.DeviceStatusActive {
			count++
		}
	}
	return count
}

func latestDeviceSeenAt(devices []domain.UserDevice) time.Time {
	var latest time.Time
	for _, device := range devices {
		if device.LastSeenAt.After(latest) {
			latest = device.LastSeenAt
		}
	}
	return latest
}

func latestTrialGrant(trials []domain.TrialGrant) domain.TrialGrant {
	if len(trials) == 0 {
		return domain.TrialGrant{}
	}
	latest := trials[0]
	for _, trial := range trials[1:] {
		if trial.CreatedAt.After(latest.CreatedAt) {
			latest = trial
		}
	}
	return latest
}

func trialStatus(trial domain.TrialGrant, now time.Time) string {
	if trial.ID == "" {
		return "none"
	}
	if trial.Status == domain.TrialGrantStatusRevoked || trial.RevokedAt != nil {
		return domain.TrialGrantStatusRevoked
	}
	if trial.ExpiresAt != nil && !trial.ExpiresAt.After(now) {
		return "expired"
	}
	return defaultString(strings.TrimSpace(trial.Status), domain.TrialGrantStatusIssued)
}

func splitCurrentAndLegacyEntitlements(grants []domain.EntitlementGrant, now time.Time) ([]string, []string) {
	current := map[string]bool{}
	legacy := map[string]bool{}
	for _, grant := range grants {
		entitlementID := strings.TrimSpace(grant.EntitlementID)
		if entitlementID == "" || !grantIsActive(grant, now) {
			continue
		}
		if IsCurrentAccessEntitlementID(entitlementID) {
			current[entitlementID] = true
		} else {
			legacy[entitlementID] = true
		}
	}
	return sortedKeys(current), sortedKeys(legacy)
}

func grantIsActive(grant domain.EntitlementGrant, now time.Time) bool {
	if grant.Status != "" && grant.Status != domain.GrantStatusActive {
		return false
	}
	if grant.RevokedAt != nil {
		return false
	}
	if !grant.StartsAt.IsZero() && grant.StartsAt.After(now) {
		return false
	}
	return grant.ExpiresAt == nil || grant.ExpiresAt.After(now)
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func maskEmail(email string) string {
	normalized := normalizeEmail(email)
	local, domainPart, ok := strings.Cut(normalized, "@")
	if !ok {
		return maskToken(normalized)
	}
	return maskToken(local) + "@" + domainPart
}

func emailDomain(email string) string {
	_, domainPart, ok := strings.Cut(normalizeEmail(email), "@")
	if !ok {
		return ""
	}
	return domainPart
}

func emailFingerprint(email string) string {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("walnut-access-admin-v1:" + normalized))
	return hex.EncodeToString(digest[:])[:12]
}

func deviceFingerprint(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("walnut-access-device-v1:" + deviceID))
	return hex.EncodeToString(digest[:])[:12]
}

func maskDisplayName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) == 1 {
		return "*"
	}
	return string(runes[0]) + "***"
}

func maskToken(value string) string {
	if value == "" {
		return ""
	}
	if !utf8.ValidString(value) {
		return "***"
	}
	runes := []rune(value)
	if len(runes) == 1 {
		return "*"
	}
	if len(runes) <= 4 {
		return string(runes[0]) + strings.Repeat("*", len(runes)-1)
	}
	return string(runes[:2]) + strings.Repeat("*", minInt(6, len(runes)-4)) + string(runes[len(runes)-2:])
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatTime(*value)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
