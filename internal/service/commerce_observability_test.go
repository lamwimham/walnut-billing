package service

import (
	"context"
	"errors"
	"testing"

	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type spyCommerceObserver struct {
	checkout       []CheckoutObservation
	paymentEvents  []PaymentEventObservation
	fulfillments   []FulfillmentObservation
	paymentAdjusts []PaymentAdjustmentObservation
}

func (s *spyCommerceObserver) ObserveCheckout(ctx context.Context, observation CheckoutObservation) {
	s.checkout = append(s.checkout, observation)
}

func (s *spyCommerceObserver) ObservePaymentEvent(ctx context.Context, observation PaymentEventObservation) {
	s.paymentEvents = append(s.paymentEvents, observation)
}

func (s *spyCommerceObserver) ObserveFulfillment(ctx context.Context, observation FulfillmentObservation) {
	s.fulfillments = append(s.fulfillments, observation)
}

func (s *spyCommerceObserver) ObservePaymentAdjustment(ctx context.Context, observation PaymentAdjustmentObservation) {
	s.paymentAdjusts = append(s.paymentAdjusts, observation)
}

type stubCheckoutService struct {
	result *CheckoutResult
	err    error
	calls  int
}

func (s *stubCheckoutService) CreateCheckoutSession(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	s.calls++
	return s.result, s.err
}

type stubPaymentEventService struct {
	result *PaymentEventProcessResult
	err    error
	calls  int
}

func (s *stubPaymentEventService) ReceiveWebhook(ctx context.Context, input PaymentWebhookInput) (*PaymentEventProcessResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *stubPaymentEventService) Process(ctx context.Context, eventID string) (*PaymentEventProcessResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *stubPaymentEventService) ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	return nil, nil
}

func (s *stubPaymentEventService) GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error) {
	return nil, nil
}

type stubFulfillmentService struct {
	result *FulfillmentResult
	err    error
	calls  int
}

func (s *stubFulfillmentService) FulfillOrder(ctx context.Context, order *domain.Order) (*FulfillmentResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *stubFulfillmentService) ListExecutions(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error) {
	return nil, nil
}

type stubPaymentAdjustmentService struct {
	result *PaymentAdjustmentResult
	err    error
	calls  int
}

func (s *stubPaymentAdjustmentService) Apply(ctx context.Context, event *domain.PaymentEventInbox) (*PaymentAdjustmentResult, error) {
	s.calls++
	return s.result, s.err
}

func TestObservedCheckoutService_EmitsSuccessAndPreservesResult(t *testing.T) {
	observer := &spyCommerceObserver{}
	result := &CheckoutResult{Order: &domain.Order{
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    "editorial_studio_monthly",
		Provider:   "mock",
		Status:     domain.OrderStatusCheckoutCreated,
	}}
	next := &stubCheckoutService{result: result}
	svc := NewObservedCheckoutService(next, observer)

	got, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:   "usr_1",
		SKUCode:  "editorial_studio_monthly",
		Provider: "mock",
	})
	if err != nil || got != result || next.calls != 1 {
		t.Fatalf("expected wrapped result and one call, got result=%#v err=%v calls=%d", got, err, next.calls)
	}
	if len(observer.checkout) != 1 {
		t.Fatalf("expected one checkout observation, got %d", len(observer.checkout))
	}
	obs := observer.checkout[0]
	if obs.OutTradeNo != "CHK-1" || obs.Status != domain.OrderStatusCheckoutCreated || obs.ErrorKind != "none" {
		t.Fatalf("unexpected checkout observation: %#v", obs)
	}
}

func TestObservedCheckoutService_ClassifiesRiskBlock(t *testing.T) {
	observer := &spyCommerceObserver{}
	rejection := &CheckoutPolicyRejection{
		Cause: ErrCheckoutBlockedByRisk,
		Decision: CheckoutPolicyDecision{
			Allowed: false,
			Reason:  CheckoutPolicyReasonOpenPaymentRisk,
			Action:  CheckoutPolicyActionManualReview,
		},
	}
	svc := NewObservedCheckoutService(&stubCheckoutService{err: rejection}, observer)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:   "usr_1",
		SKUCode:  "editorial_studio_monthly",
		Provider: "mock",
	})
	if !errors.Is(err, ErrCheckoutBlockedByRisk) {
		t.Fatalf("expected risk block error, got %v", err)
	}
	if len(observer.checkout) != 1 {
		t.Fatalf("expected one checkout observation, got %d", len(observer.checkout))
	}
	obs := observer.checkout[0]
	if obs.Status != ObservationStatusBlocked || !obs.Blocked || obs.ErrorKind != "blocked_by_payment_risk" || obs.PolicyReason != CheckoutPolicyReasonOpenPaymentRisk || obs.PolicyAction != CheckoutPolicyActionManualReview {
		t.Fatalf("unexpected blocked observation: %#v", obs)
	}
}

func TestObservedPaymentEventService_ClassifiesFailedReceiveWithoutRawPayload(t *testing.T) {
	observer := &spyCommerceObserver{}
	svc := NewObservedPaymentEventService(&stubPaymentEventService{err: errors.New("creem webhook signature verification failed")}, observer)

	_, err := svc.ReceiveWebhook(context.Background(), PaymentWebhookInput{
		Provider:   "creem",
		RawPayload: []byte(`{"secret":"must-not-be-observed"}`),
	})
	if err == nil {
		t.Fatal("expected receive error")
	}
	if len(observer.paymentEvents) != 1 {
		t.Fatalf("expected one payment event observation, got %d", len(observer.paymentEvents))
	}
	obs := observer.paymentEvents[0]
	if obs.Operation != "receive" || obs.Provider != "creem" || obs.InboxStatus != domain.PaymentEventStatusFailed || obs.ErrorKind != "signature_verification_failed" {
		t.Fatalf("unexpected payment event observation: %#v", obs)
	}
	if obs.ProcessNote != "" || obs.ProviderEventID != "" || obs.OutTradeNo != "" {
		t.Fatalf("observation should not expose raw payload-derived details on verification failure: %#v", obs)
	}
}

func TestObservedFulfillmentService_EmitsResultDetails(t *testing.T) {
	observer := &spyCommerceObserver{}
	order := &domain.Order{OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: "editorial_studio_monthly", OrderType: domain.OrderTypeCheckout}
	result := &FulfillmentResult{
		Order:            &domain.Order{OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: "editorial_studio_monthly", OrderType: domain.OrderTypeCheckout, Status: domain.OrderStatusFulfilled},
		AlreadyFulfilled: true,
		Executions:       []domain.FulfillmentExecution{{ID: "ful_1"}, {ID: "ful_2"}},
	}
	svc := NewObservedFulfillmentService(&stubFulfillmentService{result: result}, observer)

	got, err := svc.FulfillOrder(context.Background(), order)
	if err != nil || got != result {
		t.Fatalf("expected fulfillment result, got result=%#v err=%v", got, err)
	}
	if len(observer.fulfillments) != 1 {
		t.Fatalf("expected one fulfillment observation, got %d", len(observer.fulfillments))
	}
	obs := observer.fulfillments[0]
	if obs.Status != domain.OrderStatusFulfilled || obs.ExecutionCount != 2 || !obs.AlreadyFulfilled || obs.ErrorKind != "none" {
		t.Fatalf("unexpected fulfillment observation: %#v", obs)
	}
}

func TestObservedPaymentAdjustmentService_ClassifiesPolicyStatus(t *testing.T) {
	observer := &spyCommerceObserver{}
	result := &PaymentAdjustmentResult{
		Order: &domain.Order{OutTradeNo: "CHK-1"},
		PolicyDecision: PaymentAdjustmentPolicyDecision{
			Action: PaymentAdjustmentActionManualReview,
			Reason: PaymentAdjustmentReasonRefundOutOfWindow,
		},
	}
	err := newPaymentEventPolicyError(domain.PaymentEventStatusReviewRequired, ErrPaymentAdjustmentManualReview)
	svc := NewObservedPaymentAdjustmentService(&stubPaymentAdjustmentService{result: result, err: err}, observer)

	_, gotErr := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_1",
		EventType:       domain.PaymentEventTypeRefunded,
		OutTradeNo:      "CHK-1",
	})
	if !errors.Is(gotErr, ErrPaymentAdjustmentManualReview) {
		t.Fatalf("expected manual review error, got %v", gotErr)
	}
	if len(observer.paymentAdjusts) != 1 {
		t.Fatalf("expected one adjustment observation, got %d", len(observer.paymentAdjusts))
	}
	obs := observer.paymentAdjusts[0]
	if obs.Status != domain.PaymentEventStatusReviewRequired || obs.ErrorKind != "manual_review" || obs.PolicyAction != PaymentAdjustmentActionManualReview || obs.PolicyReason != PaymentAdjustmentReasonRefundOutOfWindow {
		t.Fatalf("unexpected adjustment observation: %#v", obs)
	}
}
