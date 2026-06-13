package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrPaymentRiskFlagNotFound = errors.New("payment risk flag not found")
	ErrInvalidPaymentRiskFlag  = errors.New("invalid payment risk flag")
)

type PaymentRiskService interface {
	ListFlags(ctx context.Context, query repository.PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error)
	GetFlag(ctx context.Context, id string) (*domain.PaymentRiskFlag, error)
	ResolveFlag(ctx context.Context, input ResolvePaymentRiskFlagInput) (*domain.PaymentRiskFlag, error)
}

type ResolvePaymentRiskFlagInput struct {
	ID         string
	ResolvedBy string
	Note       string
}

type paymentRiskServiceImpl struct {
	flags repository.PaymentRiskFlagRepository
}

func NewPaymentRiskService(flags repository.PaymentRiskFlagRepository) PaymentRiskService {
	return &paymentRiskServiceImpl{flags: flags}
}

func (s *paymentRiskServiceImpl) ListFlags(ctx context.Context, query repository.PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error) {
	if s == nil || s.flags == nil {
		return nil, ErrInvalidPaymentRiskFlag
	}
	query.UserID = strings.TrimSpace(query.UserID)
	query.OutTradeNo = strings.TrimSpace(query.OutTradeNo)
	query.Provider = strings.TrimSpace(query.Provider)
	query.Reason = strings.TrimSpace(query.Reason)
	query.Severity = strings.TrimSpace(query.Severity)
	query.Status = strings.TrimSpace(query.Status)
	return s.flags.List(ctx, query)
}

func (s *paymentRiskServiceImpl) GetFlag(ctx context.Context, id string) (*domain.PaymentRiskFlag, error) {
	if s == nil || s.flags == nil || strings.TrimSpace(id) == "" {
		return nil, ErrPaymentRiskFlagNotFound
	}
	flag, err := s.flags.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrPaymentRiskFlagNotFound
		}
		return nil, err
	}
	return flag, nil
}

func (s *paymentRiskServiceImpl) ResolveFlag(ctx context.Context, input ResolvePaymentRiskFlagInput) (*domain.PaymentRiskFlag, error) {
	flag, err := s.GetFlag(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	if flag.Status == domain.PaymentRiskStatusResolved {
		return flag, nil
	}
	now := time.Now().UTC()
	flag.Status = domain.PaymentRiskStatusResolved
	flag.ResolvedAt = &now
	flag.UpdatedAt = now
	flag.ResolvedBy = defaultString(strings.TrimSpace(input.ResolvedBy), "admin")
	flag.Note = appendPaymentRiskResolutionNote(flag.Note, input.Note)
	if err := s.flags.Update(ctx, flag); err != nil {
		return nil, err
	}
	return flag, nil
}

func appendPaymentRiskResolutionNote(existing string, note string) string {
	existing = strings.TrimSpace(existing)
	note = strings.TrimSpace(note)
	if note == "" {
		return existing
	}
	resolution := "resolution: " + note
	if existing == "" {
		return resolution
	}
	return existing + "\n" + resolution
}
