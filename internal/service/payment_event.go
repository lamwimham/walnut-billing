package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidPaymentEvent        = errors.New("invalid payment event")
	ErrPaymentEventNotFound       = errors.New("payment event not found")
	ErrPaymentEventNotProcessable = errors.New("payment event is not processable")
	ErrPaymentAmountMismatch      = errors.New("payment amount mismatch")
	ErrPaymentCurrencyMismatch    = errors.New("payment currency mismatch")
)

// PaymentWebhookGateway is the narrow payment-provider boundary consumed by the
// webhook inbox. payment.PaymentService implements this without exposing provider
// internals to application services.
type PaymentWebhookGateway interface {
	VerifyWebhookEvent(ctx context.Context, providerName string, req payment.WebhookVerificationRequest) (*payment.VerifiedWebhookEvent, error)
}

type PaymentWebhookInput struct {
	Provider   string
	Headers    map[string]string
	Params     map[string]string
	RawPayload []byte
}

type PaymentEventProcessResult struct {
	Event       *domain.PaymentEventInbox `json:"event"`
	Duplicate   bool                      `json:"duplicate"`
	Processed   bool                      `json:"processed"`
	ProcessNote string                    `json:"process_note,omitempty"`
}

type PaymentEventService interface {
	ReceiveWebhook(ctx context.Context, input PaymentWebhookInput) (*PaymentEventProcessResult, error)
	Process(ctx context.Context, eventID string) (*PaymentEventProcessResult, error)
	ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error)
	GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error)
}

type PaymentEventProcessor interface {
	ProcessPaymentEvent(ctx context.Context, event *domain.PaymentEventInbox) error
}

// PaymentEventPolicyError lets processors return a stable terminal inbox status
// without coupling PaymentEventService to processor-specific error types.
type PaymentEventPolicyError struct {
	Status string
	Cause  error
}

func newPaymentEventPolicyError(status string, cause error) error {
	return &PaymentEventPolicyError{Status: status, Cause: cause}
}

func (e *PaymentEventPolicyError) Error() string {
	if e == nil || e.Cause == nil {
		return "payment event policy decision"
	}
	return e.Cause.Error()
}

func (e *PaymentEventPolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type paymentEventServiceImpl struct {
	events    repository.PaymentEventRepository
	gateway   PaymentWebhookGateway
	processor PaymentEventProcessor
}

func NewPaymentEventService(
	events repository.PaymentEventRepository,
	gateway PaymentWebhookGateway,
	processor PaymentEventProcessor,
) PaymentEventService {
	return &paymentEventServiceImpl{events: events, gateway: gateway, processor: processor}
}

func (s *paymentEventServiceImpl) ReceiveWebhook(ctx context.Context, input PaymentWebhookInput) (*PaymentEventProcessResult, error) {
	input.Provider = strings.TrimSpace(input.Provider)
	if input.Provider == "" || s.events == nil || s.gateway == nil {
		return nil, ErrInvalidPaymentEvent
	}
	verified, err := s.gateway.VerifyWebhookEvent(ctx, input.Provider, payment.WebhookVerificationRequest{
		Headers:    input.Headers,
		Params:     input.Params,
		RawPayload: input.RawPayload,
	})
	if err != nil {
		return nil, err
	}
	event, err := s.eventFromVerified(input.Provider, input.RawPayload, verified)
	if err != nil {
		return nil, err
	}

	existing, err := s.events.GetByProviderEventID(ctx, event.Provider, event.ProviderEventID)
	if err == nil {
		if isPaymentEventTerminal(existing.Status) {
			return &PaymentEventProcessResult{Event: existing, Duplicate: true, Processed: existing.Status == domain.PaymentEventStatusProcessed}, nil
		}
		if existing.Status == domain.PaymentEventStatusProcessing {
			return &PaymentEventProcessResult{Event: existing, Duplicate: true, Processed: false, ProcessNote: "processing"}, nil
		}
		result, processErr := s.processEvent(ctx, existing, false)
		if result != nil {
			result.Duplicate = true
		}
		return result, processErr
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if err := s.events.Create(ctx, event); err != nil {
		if existing, getErr := s.events.GetByProviderEventID(ctx, event.Provider, event.ProviderEventID); getErr == nil {
			if isPaymentEventTerminal(existing.Status) {
				return &PaymentEventProcessResult{Event: existing, Duplicate: true, Processed: existing.Status == domain.PaymentEventStatusProcessed}, nil
			}
			if existing.Status == domain.PaymentEventStatusProcessing {
				return &PaymentEventProcessResult{Event: existing, Duplicate: true, Processed: false, ProcessNote: "processing"}, nil
			}
			result, processErr := s.processEvent(ctx, existing, false)
			if result != nil {
				result.Duplicate = true
			}
			return result, processErr
		}
		return nil, err
	}
	return s.processEvent(ctx, event, false)
}

func (s *paymentEventServiceImpl) Process(ctx context.Context, eventID string) (*PaymentEventProcessResult, error) {
	if strings.TrimSpace(eventID) == "" || s.events == nil {
		return nil, ErrPaymentEventNotFound
	}
	event, err := s.events.GetByID(ctx, strings.TrimSpace(eventID))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrPaymentEventNotFound
		}
		return nil, err
	}
	return s.processEvent(ctx, event, true)
}

func (s *paymentEventServiceImpl) ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	if s.events == nil {
		return nil, ErrInvalidPaymentEvent
	}
	return s.events.List(ctx, query)
}

func (s *paymentEventServiceImpl) GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error) {
	if strings.TrimSpace(eventID) == "" || s.events == nil {
		return nil, ErrPaymentEventNotFound
	}
	event, err := s.events.GetByID(ctx, strings.TrimSpace(eventID))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrPaymentEventNotFound
		}
		return nil, err
	}
	return event, nil
}

func (s *paymentEventServiceImpl) processEvent(ctx context.Context, event *domain.PaymentEventInbox, forceReviewReprocess bool) (*PaymentEventProcessResult, error) {
	if event == nil {
		return nil, ErrPaymentEventNotFound
	}
	if isPaymentEventTerminal(event.Status) && !(forceReviewReprocess && isPaymentEventReviewStatus(event.Status)) {
		return &PaymentEventProcessResult{Event: event, Processed: event.Status == domain.PaymentEventStatusProcessed}, nil
	}
	if !isProcessablePaymentEventType(event.EventType) {
		now := time.Now().UTC()
		event.Status = domain.PaymentEventStatusIgnored
		event.ProcessedAt = &now
		event.UpdatedAt = now
		if err := s.events.Update(ctx, event); err != nil {
			return nil, err
		}
		return &PaymentEventProcessResult{Event: event, Processed: false, ProcessNote: "ignored_event_type"}, nil
	}
	if s.processor == nil {
		now := time.Now().UTC()
		event.Status = domain.PaymentEventStatusReceived
		event.UpdatedAt = now
		if err := s.events.Update(ctx, event); err != nil {
			return nil, err
		}
		return &PaymentEventProcessResult{Event: event, Processed: false, ProcessNote: "processor_unavailable"}, nil
	}

	now := time.Now().UTC()
	event.Status = domain.PaymentEventStatusProcessing
	event.Attempts++
	event.UpdatedAt = now
	if err := s.events.Update(ctx, event); err != nil {
		return nil, err
	}

	if err := s.processor.ProcessPaymentEvent(ctx, event); err != nil {
		if result, handled, handleErr := s.recordPolicyDecision(ctx, event, err); handled {
			return result, handleErr
		}
		now = time.Now().UTC()
		event.Status = domain.PaymentEventStatusFailed
		event.LastError = err.Error()
		event.UpdatedAt = now
		if updateErr := s.events.Update(ctx, event); updateErr != nil {
			return nil, updateErr
		}
		return &PaymentEventProcessResult{Event: event, Processed: false, ProcessNote: err.Error()}, err
	}

	now = time.Now().UTC()
	event.Status = domain.PaymentEventStatusProcessed
	event.LastError = ""
	event.ProcessedAt = &now
	event.UpdatedAt = now
	if err := s.events.Update(ctx, event); err != nil {
		return nil, err
	}
	return &PaymentEventProcessResult{Event: event, Processed: true}, nil
}

func (s *paymentEventServiceImpl) recordPolicyDecision(ctx context.Context, event *domain.PaymentEventInbox, err error) (*PaymentEventProcessResult, bool, error) {
	status, ok := paymentEventPolicyStatus(err)
	if !ok {
		return nil, false, nil
	}
	now := time.Now().UTC()
	event.Status = status
	event.LastError = err.Error()
	event.UpdatedAt = now
	if updateErr := s.events.Update(ctx, event); updateErr != nil {
		return nil, true, updateErr
	}
	return &PaymentEventProcessResult{Event: event, Processed: false, ProcessNote: err.Error()}, true, nil
}

func paymentEventPolicyStatus(err error) (string, bool) {
	policyErr, ok := paymentEventPolicyErrorFrom(err)
	if !ok || !isPaymentEventReviewStatus(policyErr.Status) {
		return "", false
	}
	return policyErr.Status, true
}

func paymentEventPolicyErrorFrom(err error) (*PaymentEventPolicyError, bool) {
	var policyErr *PaymentEventPolicyError
	if !errors.As(err, &policyErr) || policyErr == nil {
		return nil, false
	}
	return policyErr, true
}

func (s *paymentEventServiceImpl) eventFromVerified(provider string, rawPayload []byte, verified *payment.VerifiedWebhookEvent) (*domain.PaymentEventInbox, error) {
	if verified == nil {
		return nil, ErrInvalidPaymentEvent
	}
	providerEventID := strings.TrimSpace(verified.ProviderEventID)
	if providerEventID == "" || strings.TrimSpace(verified.EventType) == "" || !verified.SignatureVerified {
		return nil, ErrInvalidPaymentEvent
	}
	now := time.Now().UTC()
	eventID, err := generateEntityID("pev_")
	if err != nil {
		return nil, err
	}
	raw := verified.RawPayload
	if raw == "" && len(rawPayload) > 0 {
		raw = string(rawPayload)
	}
	return &domain.PaymentEventInbox{
		ID:                eventID,
		Provider:          provider,
		ProviderEventID:   providerEventID,
		EventType:         strings.TrimSpace(verified.EventType),
		OutTradeNo:        strings.TrimSpace(verified.OutTradeNo),
		ProviderTradeNo:   strings.TrimSpace(verified.ProviderTradeNo),
		Amount:            verified.Amount,
		Currency:          strings.TrimSpace(verified.Currency),
		PeriodStartAt:     verified.PeriodStartAt,
		PeriodEndAt:       verified.PeriodEndAt,
		SignatureVerified: verified.SignatureVerified,
		PayloadHash:       payloadHash(raw),
		RawPayload:        raw,
		Status:            domain.PaymentEventStatusReceived,
		ReceivedAt:        now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func payloadHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func isProcessablePaymentEventType(eventType string) bool {
	switch eventType {
	case domain.PaymentEventTypePaid,
		domain.PaymentEventTypeCancelled,
		domain.PaymentEventTypeRefunded,
		domain.PaymentEventTypeDisputed,
		domain.PaymentEventTypeRenewalPaid,
		domain.PaymentEventTypeRenewalFailed,
		domain.PaymentEventTypeSubscriptionExpired:
		return true
	default:
		return false
	}
}

func isPaymentEventTerminal(status string) bool {
	switch status {
	case domain.PaymentEventStatusProcessed, domain.PaymentEventStatusIgnored, domain.PaymentEventStatusReviewRequired, domain.PaymentEventStatusPolicyRejected:
		return true
	default:
		return false
	}
}

func isPaymentEventReviewStatus(status string) bool {
	switch status {
	case domain.PaymentEventStatusReviewRequired, domain.PaymentEventStatusPolicyRejected:
		return true
	default:
		return false
	}
}
