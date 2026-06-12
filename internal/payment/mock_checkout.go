package payment

import (
	"context"
	"fmt"
	"strings"
)

// CheckoutMockAdapter is a provider-agnostic mock for local commerce checkout.
// It implements both the legacy PaymentProvider contract and the hosted
// CheckoutProvider extension, so M6 checkout flows can be tested without a real provider.
type CheckoutMockAdapter struct {
	NotifyURL string
	BaseURL   string
}

func NewCheckoutMockAdapter(notifyURL string) *CheckoutMockAdapter {
	return &CheckoutMockAdapter{
		NotifyURL: notifyURL,
		BaseURL:   "https://mock.checkout.walnut.local",
	}
}

func (m *CheckoutMockAdapter) Name() string {
	return "mock"
}

func (m *CheckoutMockAdapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return strings.TrimRight(m.BaseURL, "/") + "/pay/" + outTradeNo, nil
}

func (m *CheckoutMockAdapter) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error) {
	if strings.TrimSpace(req.OutTradeNo) == "" {
		return nil, fmt.Errorf("out_trade_no is required")
	}
	return &CheckoutSession{
		CheckoutURL:        strings.TrimRight(m.BaseURL, "/") + "/checkout/" + req.OutTradeNo,
		ProviderCheckoutID: "mock_chk_" + req.OutTradeNo,
		ProviderCustomerID: "mock_cus_" + defaultMockCustomer(req.UserID),
		Status:             "checkout_created",
	}, nil
}

func (m *CheckoutMockAdapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	outTradeNo = params["out_trade_no"]
	providerTradeNo = params["transaction_id"]
	if providerTradeNo == "" {
		providerTradeNo = params["trade_no"]
	}
	if outTradeNo == "" {
		return "", "", 0, fmt.Errorf("missing out_trade_no in callback")
	}
	if providerTradeNo == "" {
		providerTradeNo = "mock_trade_" + outTradeNo
	}
	return outTradeNo, providerTradeNo, 0, nil
}

func (m *CheckoutMockAdapter) BuildSuccessResponse() (contentType string, body string) {
	return "application/json", `{"status":"ok"}`
}

func (m *CheckoutMockAdapter) BuildFailureResponse() (contentType string, body string) {
	return "application/json", `{"status":"fail"}`
}

func defaultMockCustomer(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "anonymous"
	}
	return userID
}
