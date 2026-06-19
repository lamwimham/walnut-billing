package service

import (
	"context"
	"errors"
	"testing"

	"walnut-billing/internal/domain"
)

type spyOperationsObserver struct {
	subscriptions []SubscriptionActionObservation
	cloudSyncs    []CloudSyncObservation
	snapshots     []AccessSnapshotObservation
}

func (s *spyOperationsObserver) ObserveSubscriptionAction(ctx context.Context, observation SubscriptionActionObservation) {
	s.subscriptions = append(s.subscriptions, observation)
}

func (s *spyOperationsObserver) ObserveCloudSync(ctx context.Context, observation CloudSyncObservation) {
	s.cloudSyncs = append(s.cloudSyncs, observation)
}

func (s *spyOperationsObserver) ObserveAccessSnapshot(ctx context.Context, observation AccessSnapshotObservation) {
	s.snapshots = append(s.snapshots, observation)
}

type stubSubscriptionCancellationService struct {
	result *SubscriptionCancellationResult
	err    error
}

func (s *stubSubscriptionCancellationService) Cancel(ctx context.Context, input SubscriptionCancellationInput) (*SubscriptionCancellationResult, error) {
	return s.result, s.err
}

func (s *stubSubscriptionCancellationService) Resume(ctx context.Context, input SubscriptionResumeInput) (*SubscriptionCancellationResult, error) {
	return s.result, s.err
}

type stubCloudStorageService struct {
	authorizeResult *CloudSyncAuthorization
	commitResult    *CloudManifestCommitResult
	usageResult     *CloudStorageUsage
	err             error
}

func (s *stubCloudStorageService) AuthorizeSync(ctx context.Context, input CloudSyncAuthorizationInput) (*CloudSyncAuthorization, error) {
	return s.authorizeResult, s.err
}

func (s *stubCloudStorageService) CommitManifest(ctx context.Context, input CloudManifestCommitInput) (*CloudManifestCommitResult, error) {
	return s.commitResult, s.err
}

func (s *stubCloudStorageService) Usage(ctx context.Context, userID string) (*CloudStorageUsage, error) {
	return s.usageResult, s.err
}

func (s *stubCloudStorageService) ListProjects(ctx context.Context, query CloudStorageProjectQuery) (*CloudStorageProjectList, error) {
	return &CloudStorageProjectList{UserID: query.UserID}, s.err
}

func (s *stubCloudStorageService) LatestManifest(ctx context.Context, query CloudStorageLatestManifestQuery) (*CloudStorageLatestManifest, error) {
	return &CloudStorageLatestManifest{}, s.err
}

type stubAccessSnapshotIssuer struct {
	result *domain.AccessSnapshotV2
	err    error
}

func (s *stubAccessSnapshotIssuer) Issue(ctx context.Context, input AccessSnapshotIssueInput) (*domain.AccessSnapshotV2, error) {
	return s.result, s.err
}

func TestObservedSubscriptionCancellationService_EmitsFailureClassification(t *testing.T) {
	observer := &spyOperationsObserver{}
	svc := NewObservedSubscriptionCancellationService(&stubSubscriptionCancellationService{err: ErrSubscriptionControlFailed}, observer)

	_, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly})
	if !errors.Is(err, ErrSubscriptionControlFailed) {
		t.Fatalf("expected control failure, got %v", err)
	}
	if len(observer.subscriptions) != 1 {
		t.Fatalf("expected one subscription observation, got %d", len(observer.subscriptions))
	}
	obs := observer.subscriptions[0]
	if obs.Operation != "cancel" || obs.Status != ObservationStatusFailed || obs.ErrorKind != "control_failed" || obs.SKUCode != domain.SKUProOwnAIMonthly {
		t.Fatalf("unexpected subscription observation: %#v", obs)
	}
}

func TestObservedCloudStorageService_ClassifiesOverQuotaWithoutObjectKeys(t *testing.T) {
	observer := &spyOperationsObserver{}
	svc := NewObservedCloudStorageService(&stubCloudStorageService{err: ErrCloudStorageOverQuota}, observer)

	_, err := svc.AuthorizeSync(context.Background(), CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "local-project",
		Resources: []CloudResourceDescriptor{{
			ResourceID:  "wiki/page.md",
			ContentHash: "sha256:page",
			SizeBytes:   1200,
		}},
	})
	if !errors.Is(err, ErrCloudStorageOverQuota) {
		t.Fatalf("expected over quota, got %v", err)
	}
	if len(observer.cloudSyncs) != 1 {
		t.Fatalf("expected one cloud observation, got %d", len(observer.cloudSyncs))
	}
	obs := observer.cloudSyncs[0]
	if obs.Operation != "authorize_sync" || obs.Status != ObservationStatusBlocked || obs.ErrorKind != "over_quota" || !obs.OverQuota || obs.RequestedBytes != 1200 {
		t.Fatalf("unexpected cloud observation: %#v", obs)
	}
	if obs.CloudProjectID != "" || obs.Provider != "" {
		t.Fatalf("over-quota observation should not invent storage/provider details: %#v", obs)
	}
}

func TestObservedAccessSnapshotIssuer_ClassifiesSigningFailureWithoutRawDeviceID(t *testing.T) {
	observer := &spyOperationsObserver{}
	svc := NewObservedAccessSnapshotIssuer(&stubAccessSnapshotIssuer{err: ErrSnapshotSignature}, observer)

	_, err := svc.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1", DeviceID: "raw-device-1"})
	if !errors.Is(err, ErrSnapshotSignature) {
		t.Fatalf("expected signature error, got %v", err)
	}
	if len(observer.snapshots) != 1 {
		t.Fatalf("expected one snapshot observation, got %d", len(observer.snapshots))
	}
	obs := observer.snapshots[0]
	if obs.Status != ObservationStatusFailed || obs.ErrorKind != "signature_error" || !obs.DevicePresent || obs.DeviceStatus != "" {
		t.Fatalf("unexpected snapshot observation: %#v", obs)
	}
}
