package payment

import "context"

// PaymentProvider defines the interface for payment gateway adapters.
// Each payment provider (WeChat, Alipay) implements this interface.
type PaymentProvider interface {
	// Name returns the provider identifier (e.g., "wechat", "alipay").
	Name() string

	// CreatePaymentURL generates a payment URL for the given order.
	// Returns the URL the user should visit/scan to pay.
	CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error)

	// VerifyCallback validates the payment callback signature and returns the trade info.
	// Returns (outTradeNo, providerTradeNo, amount, error).
	VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error)

	// BuildSuccessResponse returns the provider-specific success response body.
	// WeChat expects XML, Alipay expects plain text "success".
	BuildSuccessResponse() (contentType string, body string)

	// BuildFailureResponse returns the provider-specific failure response body.
	BuildFailureResponse() (contentType string, body string)
}
