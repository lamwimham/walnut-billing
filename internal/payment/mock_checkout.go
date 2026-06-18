package payment

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const defaultCheckoutMockBaseURL = "https://mock.checkout.walnut.local"

// CheckoutMockAdapter is a provider-agnostic mock for local commerce checkout.
// It implements both the legacy PaymentProvider contract and the hosted
// CheckoutProvider extension, so M6 checkout flows can be tested without a real provider.
type CheckoutMockAdapter struct {
	NotifyURL string
	BaseURL   string
}

func NewCheckoutMockAdapter(notifyURL string) *CheckoutMockAdapter {
	return NewCheckoutMockAdapterWithBaseURL(notifyURL, "")
}

func NewCheckoutMockAdapterWithBaseURL(notifyURL string, baseURL string) *CheckoutMockAdapter {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultCheckoutMockBaseURL
	}
	return &CheckoutMockAdapter{
		NotifyURL: strings.TrimSpace(notifyURL),
		BaseURL:   strings.TrimRight(baseURL, "/"),
	}
}

func (m *CheckoutMockAdapter) Name() string {
	return "mock"
}

func (m *CheckoutMockAdapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return strings.TrimRight(m.BaseURL, "/") + "/pay/" + url.PathEscape(outTradeNo), nil
}

func (m *CheckoutMockAdapter) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error) {
	outTradeNo := strings.TrimSpace(req.OutTradeNo)
	if outTradeNo == "" {
		return nil, fmt.Errorf("out_trade_no is required")
	}
	return &CheckoutSession{
		CheckoutURL:        m.checkoutURL(req),
		ProviderCheckoutID: "mock_chk_" + outTradeNo,
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

func (m *CheckoutMockAdapter) checkoutURL(req CheckoutRequest) string {
	baseURL := strings.TrimRight(m.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCheckoutMockBaseURL
	}
	checkoutURL := baseURL + "/checkout/" + url.PathEscape(strings.TrimSpace(req.OutTradeNo))
	query := url.Values{}
	if successURL := strings.TrimSpace(req.SuccessURL); successURL != "" {
		query.Set("success_url", successURL)
	}
	if cancelURL := strings.TrimSpace(req.CancelURL); cancelURL != "" {
		query.Set("cancel_url", cancelURL)
	}
	if skuCode := strings.TrimSpace(req.SKUCode); skuCode != "" {
		query.Set("sku_code", skuCode)
	}
	if userID := strings.TrimSpace(req.UserID); userID != "" {
		query.Set("user_id", userID)
	}
	if encoded := query.Encode(); encoded != "" {
		checkoutURL += "?" + encoded
	}
	return checkoutURL
}

func defaultMockCustomer(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "anonymous"
	}
	return userID
}
