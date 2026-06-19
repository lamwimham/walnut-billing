package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"walnut-billing/internal/domain"
)

type SubscriptionActionObservation struct {
	Operation           string
	UserID              string
	SKUCode             string
	Status              string
	ErrorKind           string
	CancelAtPeriodEnd   bool
	CurrentPeriodEndsAt string
	Duration            time.Duration
}

type CloudSyncObservation struct {
	Operation       string
	Provider        string
	UserID          string
	ClientProjectID string
	CloudProjectID  string
	Status          string
	ErrorKind       string
	RequestedBytes  int64
	UsedBytes       int64
	QuotaBytes      int64
	OverQuota       bool
	Duration        time.Duration
}

type AccessSnapshotObservation struct {
	UserID         string
	DevicePresent  bool
	DeviceStatus   string
	Status         string
	ErrorKind      string
	SignatureKeyID string
	SignatureAlg   string
	LicenseState   string
	Duration       time.Duration
}

type SubscriptionActionObserver interface {
	ObserveSubscriptionAction(ctx context.Context, observation SubscriptionActionObservation)
}

type CloudSyncObserver interface {
	ObserveCloudSync(ctx context.Context, observation CloudSyncObservation)
}

type AccessSnapshotObserver interface {
	ObserveAccessSnapshot(ctx context.Context, observation AccessSnapshotObservation)
}

type observedSubscriptionCancellationService struct {
	next     SubscriptionCancellationService
	observer SubscriptionActionObserver
}

func NewObservedSubscriptionCancellationService(next SubscriptionCancellationService, observer SubscriptionActionObserver) SubscriptionCancellationService {
	if next == nil || observer == nil {
		return next
	}
	return &observedSubscriptionCancellationService{next: next, observer: observer}
}

func (s *observedSubscriptionCancellationService) Cancel(ctx context.Context, input SubscriptionCancellationInput) (*SubscriptionCancellationResult, error) {
	started := time.Now()
	result, err := s.next.Cancel(ctx, input)
	if s.observer != nil {
		s.observer.ObserveSubscriptionAction(ctx, subscriptionActionObservation("cancel", input.UserID, input.SKUCode, result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedSubscriptionCancellationService) Resume(ctx context.Context, input SubscriptionResumeInput) (*SubscriptionCancellationResult, error) {
	started := time.Now()
	result, err := s.next.Resume(ctx, input)
	if s.observer != nil {
		s.observer.ObserveSubscriptionAction(ctx, subscriptionActionObservation("resume", input.UserID, input.SKUCode, result, err, time.Since(started)))
	}
	return result, err
}

type observedCloudStorageService struct {
	next     CloudStorageService
	observer CloudSyncObserver
}

func NewObservedCloudStorageService(next CloudStorageService, observer CloudSyncObserver) CloudStorageService {
	if next == nil || observer == nil {
		return next
	}
	return &observedCloudStorageService{next: next, observer: observer}
}

func (s *observedCloudStorageService) AuthorizeSync(ctx context.Context, input CloudSyncAuthorizationInput) (*CloudSyncAuthorization, error) {
	started := time.Now()
	result, err := s.next.AuthorizeSync(ctx, input)
	if s.observer != nil {
		s.observer.ObserveCloudSync(ctx, cloudSyncAuthorizationObservation(input, result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedCloudStorageService) CommitManifest(ctx context.Context, input CloudManifestCommitInput) (*CloudManifestCommitResult, error) {
	started := time.Now()
	result, err := s.next.CommitManifest(ctx, input)
	if s.observer != nil {
		s.observer.ObserveCloudSync(ctx, cloudSyncCommitObservation(input, result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedCloudStorageService) Usage(ctx context.Context, userID string) (*CloudStorageUsage, error) {
	return s.next.Usage(ctx, userID)
}

func (s *observedCloudStorageService) ListProjects(ctx context.Context, query CloudStorageProjectQuery) (*CloudStorageProjectList, error) {
	return s.next.ListProjects(ctx, query)
}

func (s *observedCloudStorageService) LatestManifest(ctx context.Context, query CloudStorageLatestManifestQuery) (*CloudStorageLatestManifest, error) {
	return s.next.LatestManifest(ctx, query)
}

func (s *observedCloudStorageService) BuildDownloadTarget(ctx context.Context, input CloudDownloadTargetInput) (*CloudDownloadTargetAuthorization, error) {
	started := time.Now()
	result, err := s.next.BuildDownloadTarget(ctx, input)
	if s.observer != nil {
		s.observer.ObserveCloudSync(ctx, cloudDownloadTargetObservation(input, result, err, time.Since(started)))
	}
	return result, err
}

type observedAccessSnapshotIssuer struct {
	next     AccessSnapshotIssuer
	observer AccessSnapshotObserver
}

func NewObservedAccessSnapshotIssuer(next AccessSnapshotIssuer, observer AccessSnapshotObserver) AccessSnapshotIssuer {
	if next == nil || observer == nil {
		return next
	}
	return &observedAccessSnapshotIssuer{next: next, observer: observer}
}

func (i *observedAccessSnapshotIssuer) Issue(ctx context.Context, input AccessSnapshotIssueInput) (*domain.AccessSnapshotV2, error) {
	started := time.Now()
	snapshot, err := i.next.Issue(ctx, input)
	if i.observer != nil {
		i.observer.ObserveAccessSnapshot(ctx, accessSnapshotObservation(input, snapshot, err, time.Since(started)))
	}
	return snapshot, err
}

func subscriptionActionObservation(operation string, inputUserID string, inputSKUCode string, result *SubscriptionCancellationResult, err error, duration time.Duration) SubscriptionActionObservation {
	observation := SubscriptionActionObservation{
		Operation: operation,
		UserID:    inputUserID,
		SKUCode:   inputSKUCode,
		Status:    ObservationStatusSucceeded,
		ErrorKind: "none",
		Duration:  duration,
	}
	if result != nil {
		observation.UserID = result.UserID
		observation.SKUCode = result.SKUCode
		observation.Status = result.Status
		observation.CancelAtPeriodEnd = result.CancelAtPeriodEnd
		observation.CurrentPeriodEndsAt = result.CurrentPeriodEndsAt
	}
	if err != nil {
		observation.Status = ObservationStatusFailed
		observation.ErrorKind = subscriptionActionErrorKind(err)
	}
	if observation.Status == "" {
		observation.Status = ObservationStatusSucceeded
	}
	return observation
}

func cloudSyncAuthorizationObservation(input CloudSyncAuthorizationInput, result *CloudSyncAuthorization, err error, duration time.Duration) CloudSyncObservation {
	observation := CloudSyncObservation{
		Operation:       "authorize_sync",
		UserID:          input.UserID,
		ClientProjectID: input.ClientProjectID,
		Status:          ObservationStatusSucceeded,
		ErrorKind:       "none",
		RequestedBytes:  sumResourceBytesForObservation(input.Resources),
		Duration:        duration,
	}
	if result != nil {
		observation.Provider = result.Provider
		observation.UserID = result.UserID
		observation.CloudProjectID = result.CloudProjectID
		observation.ClientProjectID = result.ClientProjectID
		observation.RequestedBytes = result.RequestedBytes
		observation.UsedBytes = result.UsedBytes
		observation.QuotaBytes = result.QuotaBytes
	}
	applyCloudSyncError(&observation, err)
	return observation
}

func cloudSyncCommitObservation(input CloudManifestCommitInput, result *CloudManifestCommitResult, err error, duration time.Duration) CloudSyncObservation {
	observation := CloudSyncObservation{
		Operation:       "commit_manifest",
		UserID:          input.UserID,
		ClientProjectID: input.ClientProjectID,
		Status:          ObservationStatusSucceeded,
		ErrorKind:       "none",
		RequestedBytes:  sumResourceBytesForObservation(input.Resources),
		Duration:        duration,
	}
	if result != nil {
		observation.UserID = result.Usage.UserID
		observation.UsedBytes = result.Usage.UsedBytes
		observation.QuotaBytes = result.Usage.QuotaBytes
		observation.OverQuota = result.Usage.OverQuota
		if result.Project != nil {
			observation.CloudProjectID = result.Project.ID
			observation.ClientProjectID = result.Project.ClientProjectID
		}
	}
	applyCloudSyncError(&observation, err)
	return observation
}

func cloudDownloadTargetObservation(input CloudDownloadTargetInput, result *CloudDownloadTargetAuthorization, err error, duration time.Duration) CloudSyncObservation {
	observation := CloudSyncObservation{
		Operation:      "download_target",
		UserID:         input.UserID,
		Status:         ObservationStatusSucceeded,
		ErrorKind:      "none",
		RequestedBytes: 0,
		Duration:       duration,
	}
	if result != nil {
		observation.UserID = result.UserID
		observation.CloudProjectID = result.CloudProjectID
		observation.ClientProjectID = result.ClientProjectID
		observation.Provider = result.DownloadTarget.Provider
		observation.RequestedBytes = result.Object.SizeBytes
	}
	applyCloudSyncError(&observation, err)
	return observation
}

func accessSnapshotObservation(input AccessSnapshotIssueInput, snapshot *domain.AccessSnapshotV2, err error, duration time.Duration) AccessSnapshotObservation {
	observation := AccessSnapshotObservation{
		UserID:        input.UserID,
		DevicePresent: strings.TrimSpace(input.DeviceID) != "",
		Status:        ObservationStatusSucceeded,
		ErrorKind:     "none",
		Duration:      duration,
	}
	if snapshot != nil {
		observation.UserID = snapshot.User.ID
		observation.DevicePresent = observation.DevicePresent || snapshot.Device.ID != "" || snapshot.Device.DeviceID != ""
		observation.DeviceStatus = snapshot.Device.Status
		observation.SignatureKeyID = snapshot.SignatureKeyID
		observation.SignatureAlg = snapshot.SignatureAlg
		observation.LicenseState = snapshot.License.State
	}
	if err != nil {
		observation.Status = ObservationStatusFailed
		observation.ErrorKind = accessSnapshotErrorKind(err)
	}
	return observation
}

func applyCloudSyncError(observation *CloudSyncObservation, err error) {
	if observation == nil || err == nil {
		return
	}
	observation.ErrorKind = cloudSyncErrorKind(err)
	if errors.Is(err, ErrCloudStorageOverQuota) {
		observation.Status = ObservationStatusBlocked
		observation.OverQuota = true
		return
	}
	if errors.Is(err, ErrCloudStorageAccessDenied) {
		observation.Status = ObservationStatusBlocked
		return
	}
	observation.Status = ObservationStatusFailed
}

func sumResourceBytesForObservation(resources []CloudResourceDescriptor) int64 {
	var total int64
	for _, resource := range resources {
		if resource.SizeBytes > 0 {
			total += resource.SizeBytes
		}
	}
	return total
}

func subscriptionActionErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrInvalidSubscriptionCancellation):
		return "invalid_request"
	case errors.Is(err, ErrSubscriptionNotFound):
		return "subscription_not_found"
	case errors.Is(err, ErrSubscriptionControlUnavailable):
		return "control_unavailable"
	case errors.Is(err, ErrSubscriptionControlFailed):
		return "control_failed"
	case errors.Is(err, ErrUserNotFound):
		return "user_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout"
	default:
		return "unknown"
	}
}

func cloudSyncErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrCloudStorageOverQuota):
		return "over_quota"
	case errors.Is(err, ErrCloudStorageAccessDenied):
		return "access_denied"
	case errors.Is(err, ErrCloudStorageProviderNotConfigured):
		return "provider_not_configured"
	case errors.Is(err, ErrInvalidCloudStorage):
		return "invalid_request"
	case errors.Is(err, ErrCloudProjectNotFound):
		return "project_not_found"
	case errors.Is(err, ErrCloudSyncSessionNotFound):
		return "sync_session_not_found"
	case errors.Is(err, ErrCloudSyncSessionExpired):
		return "sync_session_expired"
	case errors.Is(err, ErrCloudSyncSessionAlreadyCommitted):
		return "sync_session_committed"
	case errors.Is(err, ErrUserNotFound):
		return "user_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout"
	default:
		return "unknown"
	}
}

func accessSnapshotErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrSnapshotSignature):
		return "signature_error"
	case errors.Is(err, ErrInvalidAccessSnapshot):
		return "invalid_snapshot"
	case errors.Is(err, ErrAccessDeviceRevoked):
		return "device_revoked"
	case errors.Is(err, ErrUserNotFound):
		return "user_not_found"
	default:
		return "unknown"
	}
}
