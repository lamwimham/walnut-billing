package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

const (
	ObservationStatusSucceeded = "succeeded"
	ObservationStatusFailed    = "failed"
	ObservationStatusBlocked   = "blocked"
	ObservationStatusIgnored   = "ignored"
)

type CheckoutObservation struct {
	Provider     string
	SKUCode      string
	UserID       string
	OutTradeNo   string
	Status       string
	ErrorKind    string
	Blocked      bool
	Duration     time.Duration
	PolicyReason string
	PolicyAction string
}

type PaymentEventObservation struct {
	Operation       string
	Provider        string
	ProviderEventID string
	EventType       string
	OutTradeNo      string
	InboxStatus     string
	Processed       bool
	Duplicate       bool
	Attempts        int
	ErrorKind       string
	ProcessNote     string
	Duration        time.Duration
}

type FulfillmentObservation struct {
	OutTradeNo       string
	UserID           string
	SKUCode          string
	OrderType        string
	Status           string
	ErrorKind        string
	ExecutionCount   int
	AlreadyFulfilled bool
	Duration         time.Duration
}

type PaymentAdjustmentObservation struct {
	Provider          string
	ProviderEventID   string
	EventType         string
	OutTradeNo        string
	Status            string
	ErrorKind         string
	PolicyAction      string
	PolicyReason      string
	RiskFlagCreated   bool
	RevokedGrantCount int
	ClawbackCredits   int64
	Duration          time.Duration
}

type CheckoutObserver interface {
	ObserveCheckout(ctx context.Context, observation CheckoutObservation)
}

type PaymentEventObserver interface {
	ObservePaymentEvent(ctx context.Context, observation PaymentEventObservation)
}

type FulfillmentObserver interface {
	ObserveFulfillment(ctx context.Context, observation FulfillmentObservation)
}

type PaymentAdjustmentObserver interface {
	ObservePaymentAdjustment(ctx context.Context, observation PaymentAdjustmentObservation)
}

type observedCheckoutService struct {
	next     CheckoutService
	observer CheckoutObserver
}

func NewObservedCheckoutService(next CheckoutService, observer CheckoutObserver) CheckoutService {
	if next == nil || observer == nil {
		return next
	}
	return &observedCheckoutService{next: next, observer: observer}
}

func (s *observedCheckoutService) CreateCheckoutSession(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	started := time.Now()
	result, err := s.next.CreateCheckoutSession(ctx, input)
	if s.observer != nil {
		s.observer.ObserveCheckout(ctx, checkoutObservation(input, result, err, time.Since(started)))
	}
	return result, err
}

type observedPaymentEventService struct {
	next     PaymentEventService
	observer PaymentEventObserver
}

func NewObservedPaymentEventService(next PaymentEventService, observer PaymentEventObserver) PaymentEventService {
	if next == nil || observer == nil {
		return next
	}
	return &observedPaymentEventService{next: next, observer: observer}
}

func (s *observedPaymentEventService) ReceiveWebhook(ctx context.Context, input PaymentWebhookInput) (*PaymentEventProcessResult, error) {
	started := time.Now()
	result, err := s.next.ReceiveWebhook(ctx, input)
	if s.observer != nil {
		s.observer.ObservePaymentEvent(ctx, paymentEventObservation("receive", input.Provider, result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedPaymentEventService) Process(ctx context.Context, eventID string) (*PaymentEventProcessResult, error) {
	started := time.Now()
	result, err := s.next.Process(ctx, eventID)
	if s.observer != nil {
		s.observer.ObservePaymentEvent(ctx, paymentEventObservation("reprocess", "", result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedPaymentEventService) ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	return s.next.ListEvents(ctx, query)
}

func (s *observedPaymentEventService) GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error) {
	return s.next.GetEvent(ctx, eventID)
}

type observedFulfillmentService struct {
	next     FulfillmentService
	observer FulfillmentObserver
}

func NewObservedFulfillmentService(next FulfillmentService, observer FulfillmentObserver) FulfillmentService {
	if next == nil || observer == nil {
		return next
	}
	return &observedFulfillmentService{next: next, observer: observer}
}

func (s *observedFulfillmentService) FulfillOrder(ctx context.Context, order *domain.Order) (*FulfillmentResult, error) {
	started := time.Now()
	result, err := s.next.FulfillOrder(ctx, order)
	if s.observer != nil {
		s.observer.ObserveFulfillment(ctx, fulfillmentObservation(order, result, err, time.Since(started)))
	}
	return result, err
}

func (s *observedFulfillmentService) ListExecutions(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error) {
	return s.next.ListExecutions(ctx, query)
}

type observedPaymentAdjustmentService struct {
	next     PaymentAdjustmentService
	observer PaymentAdjustmentObserver
}

func NewObservedPaymentAdjustmentService(next PaymentAdjustmentService, observer PaymentAdjustmentObserver) PaymentAdjustmentService {
	if next == nil || observer == nil {
		return next
	}
	return &observedPaymentAdjustmentService{next: next, observer: observer}
}

func (s *observedPaymentAdjustmentService) Apply(ctx context.Context, event *domain.PaymentEventInbox) (*PaymentAdjustmentResult, error) {
	started := time.Now()
	result, err := s.next.Apply(ctx, event)
	if s.observer != nil {
		s.observer.ObservePaymentAdjustment(ctx, paymentAdjustmentObservation(event, result, err, time.Since(started)))
	}
	return result, err
}

func checkoutObservation(input CheckoutInput, result *CheckoutResult, err error, duration time.Duration) CheckoutObservation {
	observation := CheckoutObservation{
		Provider:  input.Provider,
		SKUCode:   input.SKUCode,
		UserID:    input.UserID,
		Status:    ObservationStatusSucceeded,
		Duration:  duration,
		ErrorKind: "none",
	}
	if result != nil && result.Order != nil {
		observation.OutTradeNo = result.Order.OutTradeNo
		if result.Order.Status != "" {
			observation.Status = result.Order.Status
		}
	}
	if err != nil {
		observation.Status = ObservationStatusFailed
		observation.ErrorKind = checkoutErrorKind(err)
		if errors.Is(err, ErrCheckoutBlockedByRisk) || errors.Is(err, ErrCheckoutBlockedByPlan) {
			observation.Status = ObservationStatusBlocked
			observation.Blocked = true
		}
		if decision, ok := CheckoutPolicyDecisionFromError(err); ok {
			observation.PolicyReason = decision.Reason
			observation.PolicyAction = decision.Action
		}
	}
	return observation
}

func paymentEventObservation(operation string, inputProvider string, result *PaymentEventProcessResult, err error, duration time.Duration) PaymentEventObservation {
	observation := PaymentEventObservation{
		Operation:   operation,
		Provider:    inputProvider,
		InboxStatus: ObservationStatusSucceeded,
		ErrorKind:   "none",
		Duration:    duration,
	}
	if result != nil {
		observation.Processed = result.Processed
		observation.Duplicate = result.Duplicate
		observation.ProcessNote = result.ProcessNote
		if result.Event != nil {
			observation.Provider = result.Event.Provider
			observation.ProviderEventID = result.Event.ProviderEventID
			observation.EventType = result.Event.EventType
			observation.OutTradeNo = result.Event.OutTradeNo
			observation.InboxStatus = result.Event.Status
			observation.Attempts = result.Event.Attempts
		}
	}
	if err != nil {
		observation.ErrorKind = paymentEventErrorKind(err)
		if observation.InboxStatus == "" || observation.InboxStatus == ObservationStatusSucceeded {
			observation.InboxStatus = domain.PaymentEventStatusFailed
		}
	}
	if observation.InboxStatus == "" {
		observation.InboxStatus = ObservationStatusSucceeded
	}
	return observation
}

func fulfillmentObservation(input *domain.Order, result *FulfillmentResult, err error, duration time.Duration) FulfillmentObservation {
	observation := FulfillmentObservation{
		Status:    ObservationStatusSucceeded,
		ErrorKind: "none",
		Duration:  duration,
	}
	if input != nil {
		observation.OutTradeNo = input.OutTradeNo
		observation.UserID = input.UserID
		observation.SKUCode = input.SKUCode
		observation.OrderType = input.OrderType
	}
	if result != nil {
		observation.ExecutionCount = len(result.Executions)
		observation.AlreadyFulfilled = result.AlreadyFulfilled
		if result.Order != nil {
			observation.OutTradeNo = result.Order.OutTradeNo
			observation.UserID = result.Order.UserID
			observation.SKUCode = result.Order.SKUCode
			observation.OrderType = result.Order.OrderType
			if result.Order.Status != "" {
				observation.Status = result.Order.Status
			}
		}
	}
	if err != nil {
		observation.Status = ObservationStatusFailed
		observation.ErrorKind = fulfillmentErrorKind(err)
	}
	return observation
}

func paymentAdjustmentObservation(event *domain.PaymentEventInbox, result *PaymentAdjustmentResult, err error, duration time.Duration) PaymentAdjustmentObservation {
	observation := PaymentAdjustmentObservation{
		Status:    ObservationStatusSucceeded,
		ErrorKind: "none",
		Duration:  duration,
	}
	if event != nil {
		observation.Provider = event.Provider
		observation.ProviderEventID = event.ProviderEventID
		observation.EventType = event.EventType
		observation.OutTradeNo = event.OutTradeNo
	}
	if result != nil {
		observation.PolicyAction = result.PolicyDecision.Action
		observation.PolicyReason = result.PolicyDecision.Reason
		observation.RiskFlagCreated = result.RiskFlag != nil
		observation.RevokedGrantCount = len(result.RevokedGrantIDs)
		observation.ClawbackCredits = result.ClawbackCredits
		if result.Note == "non_commerce_order_ignored" {
			observation.Status = ObservationStatusIgnored
		}
		if result.Order != nil {
			observation.OutTradeNo = result.Order.OutTradeNo
		}
	}
	if err != nil {
		observation.Status = ObservationStatusFailed
		observation.ErrorKind = paymentAdjustmentErrorKind(err)
		if status, ok := paymentEventPolicyStatus(err); ok {
			observation.Status = status
		}
	}
	return observation
}

func checkoutErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrCheckoutBlockedByRisk):
		return "blocked_by_payment_risk"
	case errors.Is(err, ErrCheckoutBlockedByPlan):
		return "blocked_by_subscription_state"
	case errors.Is(err, ErrCheckoutProviderFailed):
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "provider_timeout"
		case errors.Is(err, payment.ErrProviderNotFound):
			return "provider_not_found"
		}
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded") {
			return "provider_timeout"
		}
		return "provider_failed"
	case errors.Is(err, ErrInvalidCheckoutRequest):
		return "invalid_request"
	case errors.Is(err, ErrUserNotFound):
		return "user_not_found"
	default:
		return "unknown"
	}
}

func paymentEventErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrInvalidPaymentEvent):
		return "invalid_event"
	case errors.Is(err, ErrPaymentEventNotFound):
		return "event_not_found"
	case errors.Is(err, ErrPaymentEventNotProcessable):
		return "not_processable"
	case errors.Is(err, ErrPaymentAmountMismatch):
		return "amount_mismatch"
	case errors.Is(err, ErrPaymentCurrencyMismatch):
		return "currency_mismatch"
	case errors.Is(err, payment.ErrWebhookSignatureVerificationFailed):
		return "signature_verification_failed"
	case errors.Is(err, payment.ErrWebhookInvalidPayload):
		return "webhook_invalid_payload"
	case errors.Is(err, payment.ErrProviderNotFound):
		return "provider_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "signature"):
		return "signature_verification_failed"
	case strings.Contains(message, "amount mismatch"):
		return "amount_mismatch"
	case strings.Contains(message, "currency mismatch"):
		return "currency_mismatch"
	case strings.Contains(message, "provider") && strings.Contains(message, "not found"):
		return "provider_not_found"
	case strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded"):
		return "provider_timeout"
	default:
		return "unknown"
	}
}

func fulfillmentErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrInvalidFulfillmentOrder):
		return "invalid_order"
	case errors.Is(err, ErrFulfillmentOrderNotPaid):
		return "order_not_paid"
	case errors.Is(err, ErrFulfillmentRulesNotFound):
		return "rules_not_found"
	case errors.Is(err, ErrInvalidFulfillmentRule):
		return "invalid_rule"
	default:
		return "unknown"
	}
}

func paymentAdjustmentErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrPaymentAdjustmentManualReview):
		return "manual_review"
	case errors.Is(err, ErrPaymentAdjustmentRejected):
		return "policy_rejected"
	case errors.Is(err, ErrInvalidPaymentAdjustment):
		return "invalid_adjustment"
	default:
		return "unknown"
	}
}
