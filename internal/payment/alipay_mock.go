package payment

import (
	"context"
	"fmt"
)

// AlipayMockAdapter is a mock implementation for development/testing.
// Use AlipayV2Adapter for production.
type AlipayMockAdapter struct {
	AppID     string
	NotifyURL string
}

func NewAlipayMockAdapter(appID, notifyURL string) *AlipayMockAdapter {
	return &AlipayMockAdapter{AppID: appID, NotifyURL: notifyURL}
}

func (a *AlipayMockAdapter) Name() string {
	return "alipay"
}

func (a *AlipayMockAdapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return fmt.Sprintf("https://qr.alipay.com/%s", outTradeNo), nil
}

func (a *AlipayMockAdapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	outTradeNo = params["out_trade_no"]
	providerTradeNo = params["trade_no"]
	if outTradeNo == "" {
		return "", "", 0, fmt.Errorf("missing out_trade_no in callback")
	}
	return outTradeNo, providerTradeNo, 0, nil
}

func (a *AlipayMockAdapter) BuildSuccessResponse() (contentType string, body string) {
	return "text/plain", "success"
}

func (a *AlipayMockAdapter) BuildFailureResponse() (contentType string, body string) {
	return "text/plain", "fail"
}
