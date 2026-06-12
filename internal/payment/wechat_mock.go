package payment

import (
	"context"
	"fmt"
)

// WechatPayMockAdapter is a mock implementation for development/testing.
// Use WechatPayV3Adapter for production.
type WechatPayMockAdapter struct {
	MchID     string
	NotifyURL string
}

func NewWechatPayMockAdapter(mchID, notifyURL string) *WechatPayMockAdapter {
	return &WechatPayMockAdapter{MchID: mchID, NotifyURL: notifyURL}
}

func (w *WechatPayMockAdapter) Name() string {
	return "wechat"
}

func (w *WechatPayMockAdapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return fmt.Sprintf("weixin://wxpay/bizpayurl?sr=%s", outTradeNo), nil
}

func (w *WechatPayMockAdapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	outTradeNo = params["out_trade_no"]
	providerTradeNo = params["transaction_id"]
	if outTradeNo == "" {
		return "", "", 0, fmt.Errorf("missing out_trade_no in callback")
	}
	return outTradeNo, providerTradeNo, 0, nil
}

func (w *WechatPayMockAdapter) BuildSuccessResponse() (contentType string, body string) {
	return "application/json", `{"code":"SUCCESS","message":"成功"}`
}

func (w *WechatPayMockAdapter) BuildFailureResponse() (contentType string, body string) {
	return "application/json", `{"code":"FAIL","message":"失败"}`
}
