package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

type mockPaymentEventRepo struct {
	events map[string]*domain.PaymentEventInbox
}

func newMockPaymentEventRepo() *mockPaymentEventRepo {
	return &mockPaymentEventRepo{events: make(map[string]*domain.PaymentEventInbox)}
}

func (m *mockPaymentEventRepo) Create(ctx context.Context, event *domain.PaymentEventInbox) error {
	m.events[event.ID] = event
	return nil
}

func (m *mockPaymentEventRepo) GetByID(ctx context.Context, id string) (*domain.PaymentEventInbox, error) {
	event, ok := m.events[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return event, nil
}

func (m *mockPaymentEventRepo) GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentEventInbox, error) {
	for _, event := range m.events {
		if event.Provider == provider && event.ProviderEventID == providerEventID {
			return event, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockPaymentEventRepo) List(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	var result []domain.PaymentEventInbox
	for _, event := range m.events {
		if query.Provider != "" && event.Provider != query.Provider {
			continue
		}
		if query.Status != "" && event.Status != query.Status {
			continue
		}
		if query.EventType != "" && event.EventType != query.EventType {
			continue
		}
		if query.OutTradeNo != "" && event.OutTradeNo != query.OutTradeNo {
			continue
		}
		result = append(result, *event)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ReceivedAt.After(result[j].ReceivedAt) })
	return result, nil
}

func (m *mockPaymentEventRepo) Update(ctx context.Context, event *domain.PaymentEventInbox) error {
	m.events[event.ID] = event
	return nil
}

type mockWebhookGateway struct {
	event *payment.VerifiedWebhookEvent
	err   error
	calls int
}

func (m *mockWebhookGateway) VerifyWebhookEvent(ctx context.Context, providerName string, req payment.WebhookVerificationRequest) (*payment.VerifiedWebhookEvent, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.event, nil
}

type mockPaymentProcessor struct {
	calls int
	err   error
}

func (m *mockPaymentProcessor) ProcessPaymentEvent(ctx context.Context, event *domain.PaymentEventInbox) error {
	m.calls++
	return m.err
}

func TestPaymentEventService_ReceiveWebhookProcessesAndDeduplicates(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_1",
		EventType:         domain.PaymentEventTypePaid,
		OutTradeNo:        "CHK-1",
		ProviderTradeNo:   "txn_1",
		Amount:            1900,
		Currency:          "CNY",
		SignatureVerified: true,
		RawPayload:        `{"id":"evt_1"}`,
	}}
	processor := &mockPaymentProcessor{}
	svc := NewPaymentEventService(repo, gateway, processor)

	first, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock", RawPayload: []byte(`{"id":"evt_1"}`)})
	if err != nil {
		t.Fatalf("expected first webhook to process, got %v", err)
	}
	if !first.Processed || first.Duplicate || first.Event.Status != domain.PaymentEventStatusProcessed {
		t.Fatalf("expected processed first event, got %#v", first)
	}
	second, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock", RawPayload: []byte(`{"id":"evt_1"}`)})
	if err != nil {
		t.Fatalf("expected duplicate webhook to be safe, got %v", err)
	}
	if !second.Duplicate || !second.Processed {
		t.Fatalf("expected duplicate processed result, got %#v", second)
	}
	if len(repo.events) != 1 || processor.calls != 1 || gateway.calls != 2 {
		t.Fatalf("expected one inbox event, one processor call, two verifications")
	}
}

func TestPaymentEventService_DuplicateProcessingEventDoesNotReprocess(t *testing.T) {
	repo := newMockPaymentEventRepo()
	repo.events["pev_processing"] = &domain.PaymentEventInbox{
		ID:              "pev_processing",
		Provider:        "mock",
		ProviderEventID: "evt_processing",
		EventType:       domain.PaymentEventTypePaid,
		Status:          domain.PaymentEventStatusProcessing,
	}
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_processing",
		EventType:         domain.PaymentEventTypePaid,
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{}
	svc := NewPaymentEventService(repo, gateway, processor)

	result, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err != nil {
		t.Fatalf("expected processing duplicate to be accepted, got %v", err)
	}
	if !result.Duplicate || result.Processed || result.ProcessNote != "processing" {
		t.Fatalf("expected in-flight duplicate response, got %#v", result)
	}
	if processor.calls != 0 {
		t.Fatalf("expected in-flight duplicate not to call processor")
	}
}

func TestPaymentEventService_InvalidSignatureIsRejectedBeforeInbox(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_bad",
		EventType:         domain.PaymentEventTypePaid,
		SignatureVerified: false,
	}}
	svc := NewPaymentEventService(repo, gateway, &mockPaymentProcessor{})

	_, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if !errors.Is(err, ErrInvalidPaymentEvent) {
		t.Fatalf("expected invalid payment event, got %v", err)
	}
	if len(repo.events) != 0 {
		t.Fatalf("expected invalid event not to be stored")
	}
}

func TestPaymentEventService_AdjustmentManualReviewIsAcceptedAndAdminReprocessable(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_manual_review",
		EventType:         domain.PaymentEventTypeRefunded,
		OutTradeNo:        "CHK-REVIEW",
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{err: newPaymentEventPolicyError(domain.PaymentEventStatusReviewRequired, ErrPaymentAdjustmentManualReview)}
	svc := NewPaymentEventService(repo, gateway, processor)

	first, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err != nil {
		t.Fatalf("expected manual review to be accepted without provider error, got %v", err)
	}
	if first.Processed || first.Event.Status != domain.PaymentEventStatusReviewRequired || first.ProcessNote == "" {
		t.Fatalf("expected review-required event, got %#v", first)
	}

	duplicate, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err != nil {
		t.Fatalf("expected duplicate manual-review webhook to be accepted, got %v", err)
	}
	if !duplicate.Duplicate || duplicate.Processed || processor.calls != 1 {
		t.Fatalf("expected terminal duplicate without reprocessing, duplicate=%#v calls=%d", duplicate, processor.calls)
	}

	processor.err = nil
	reprocessed, err := svc.Process(context.Background(), first.Event.ID)
	if err != nil {
		t.Fatalf("expected admin reprocess to retry review-required event, got %v", err)
	}
	if !reprocessed.Processed || reprocessed.Event.Status != domain.PaymentEventStatusProcessed || reprocessed.Event.Attempts != 2 {
		t.Fatalf("expected processed manual-review retry, got %#v", reprocessed.Event)
	}
}

func TestPaymentEventService_AdjustmentRejectionIsAcceptedWithoutProviderRetry(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_policy_rejected",
		EventType:         domain.PaymentEventTypeRefunded,
		OutTradeNo:        "CHK-REJECT",
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{err: newPaymentEventPolicyError(domain.PaymentEventStatusPolicyRejected, ErrPaymentAdjustmentRejected)}
	svc := NewPaymentEventService(repo, gateway, processor)

	result, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err != nil {
		t.Fatalf("expected policy rejection to be accepted without provider error, got %v", err)
	}
	if result.Processed || result.Event.Status != domain.PaymentEventStatusPolicyRejected || result.ProcessNote == "" {
		t.Fatalf("expected policy-rejected event, got %#v", result)
	}
}

func TestPaymentEventService_ProcessorFailureCanBeReprocessed(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_retry",
		EventType:         domain.PaymentEventTypePaid,
		OutTradeNo:        "CHK-RETRY",
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{err: errors.New("temporary failure")}
	svc := NewPaymentEventService(repo, gateway, processor)

	first, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err == nil || first.Event.Status != domain.PaymentEventStatusFailed {
		t.Fatalf("expected failed processing, result=%#v err=%v", first, err)
	}
	processor.err = nil
	second, err := svc.Process(context.Background(), first.Event.ID)
	if err != nil {
		t.Fatalf("expected reprocess success, got %v", err)
	}
	if !second.Processed || second.Event.Status != domain.PaymentEventStatusProcessed || second.Event.Attempts != 2 {
		t.Fatalf("expected processed retry with attempts=2, got %#v", second.Event)
	}
}

func TestPaymentEventService_IgnoresUnknownEventType(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_unknown",
		EventType:         "customer.created",
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{}
	svc := NewPaymentEventService(repo, gateway, processor)

	result, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "mock"})
	if err != nil {
		t.Fatalf("expected unknown event to be ignored without error, got %v", err)
	}
	if result.Event.Status != domain.PaymentEventStatusIgnored || result.Processed {
		t.Fatalf("expected ignored event, got %#v", result)
	}
	if processor.calls != 0 {
		t.Fatalf("expected processor not to be called")
	}
}

func TestPaymentEventService_ProcessesDisputedEventType(t *testing.T) {
	repo := newMockPaymentEventRepo()
	gateway := &mockWebhookGateway{event: &payment.VerifiedWebhookEvent{
		ProviderEventID:   "evt_dispute",
		EventType:         domain.PaymentEventTypeDisputed,
		OutTradeNo:        "CHK-DISPUTE",
		SignatureVerified: true,
	}}
	processor := &mockPaymentProcessor{}
	svc := NewPaymentEventService(repo, gateway, processor)

	result, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{Provider: "creem"})
	if err != nil {
		t.Fatalf("expected disputed event to process, got %v", err)
	}
	if !result.Processed || result.Event.EventType != domain.PaymentEventTypeDisputed || processor.calls != 1 {
		t.Fatalf("expected processed disputed event, result=%#v calls=%d", result, processor.calls)
	}
}

func TestPaymentOrderEventProcessor_MarksOrderPaid(t *testing.T) {
	orders := newMockTxOrderRepo()
	orders.orders["CHK-1"] = &domain.Order{OutTradeNo: "CHK-1", Amount: 1900, Status: domain.OrderStatusCheckoutCreated}
	processor := NewPaymentOrderEventProcessor(orders)
	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:        "mock",
		EventType:       domain.PaymentEventTypePaid,
		OutTradeNo:      "CHK-1",
		ProviderTradeNo: "txn_1",
		Amount:          1900,
	})
	if err != nil {
		t.Fatalf("expected order paid, got %v", err)
	}
	order := orders.orders["CHK-1"]
	if order.Status != domain.OrderStatusPaid || order.TradeNo != "txn_1" || order.PaidAt == nil {
		t.Fatalf("expected paid order, got %#v", order)
	}
}

func TestPaymentOrderEventProcessor_MarksDisputedOrderRefunded(t *testing.T) {
	orders := newMockTxOrderRepo()
	orders.orders["CHK-1"] = &domain.Order{OutTradeNo: "CHK-1", Amount: 1900, Status: domain.OrderStatusFulfilled}
	processor := NewPaymentOrderEventProcessor(orders)
	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:   "creem",
		EventType:  domain.PaymentEventTypeDisputed,
		OutTradeNo: "CHK-1",
	})
	if err != nil {
		t.Fatalf("expected disputed order to be marked refunded, got %v", err)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusRefunded {
		t.Fatalf("expected refunded status, got %s", orders.orders["CHK-1"].Status)
	}
}

func TestPaymentOrderEventProcessor_RejectsAmountMismatch(t *testing.T) {
	orders := newMockTxOrderRepo()
	orders.orders["CHK-1"] = &domain.Order{OutTradeNo: "CHK-1", Amount: 1900, Status: domain.OrderStatusCheckoutCreated}
	processor := NewPaymentOrderEventProcessor(orders)
	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:   "mock",
		EventType:  domain.PaymentEventTypePaid,
		OutTradeNo: "CHK-1",
		Amount:     1800,
	})
	if err == nil {
		t.Fatalf("expected amount mismatch")
	}
}
