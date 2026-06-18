package payment

import (
	"context"
	"testing"
	"walnut-billing/internal/domain"
)

type creemWebhookFixture struct {
	name        string
	payload     string
	wantType    string
	wantOrderNo string
	wantTradeNo string
	wantAmount  int64
	wantPeriod  bool
}

func TestCreemWebhookFixturesNormalizeToWalnutEvents(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:           "creem_test_key",
		WebhookSecret:    "whsec_test",
		SandboxMode:      true,
		ProductIDs:       map[string]string{"pro_own_ai_monthly": "prod_monthly"},
		RequiredSKUCodes: []string{"pro_own_ai_monthly"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	fixtures := []creemWebhookFixture{
		{
			name:        "paid checkout",
			payload:     `{"id":"evt_paid_1","eventType":"checkout.completed","object":{"id":"ch_1","request_id":"CHK-PAID-1","order":{"id":"ord_paid_1","amount":500,"currency":"USD","status":"paid"},"metadata":{"walnut_out_trade_no":"CHK-PAID-1"}}}`,
			wantType:    domain.PaymentEventTypePaid,
			wantOrderNo: "CHK-PAID-1",
			wantTradeNo: "ord_paid_1",
			wantAmount:  500,
		},
		{
			name:        "refund",
			payload:     `{"id":"evt_refund_1","eventType":"refund.created","object":{"id":"rf_1","refund_amount":500,"refund_currency":"USD","metadata":{"walnut_out_trade_no":"CHK-REFUND-1"}}}`,
			wantType:    domain.PaymentEventTypeRefunded,
			wantOrderNo: "CHK-REFUND-1",
			wantTradeNo: "rf_1",
			wantAmount:  500,
		},
		{
			name:        "dispute",
			payload:     `{"id":"evt_dispute_1","eventType":"dispute.created","object":{"id":"disp_container_1","dispute":{"id":"disp_1","amount":500,"currency":"USD","metadata":{"walnut_out_trade_no":"CHK-DISPUTE-1"}}}}`,
			wantType:    domain.PaymentEventTypeDisputed,
			wantOrderNo: "CHK-DISPUTE-1",
			wantTradeNo: "disp_1",
			wantAmount:  500,
		},
		{
			name:        "subscription renewal paid",
			payload:     `{"id":"evt_renewal_paid_1","eventType":"subscription.paid","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"RNL-PAID-1"}},"order":{"id":"ord_renewal_paid_1","amount":500,"currency":"USD","period_start":1782997200000,"period_end":1785675600000},"current_period_start_date":"2026-07-02T09:00:00.000Z","current_period_end_date":"2026-08-02T09:00:00.000Z"}}`,
			wantType:    domain.PaymentEventTypeRenewalPaid,
			wantOrderNo: "RNL-PAID-1",
			wantTradeNo: "ord_renewal_paid_1",
			wantAmount:  500,
			wantPeriod:  true,
		},
		{
			name:        "subscription renewal failed",
			payload:     `{"id":"evt_renewal_failed_1","eventType":"subscription.past_due","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"RNL-FAILED-1"}},"order":{"id":"ord_renewal_failed_1","amount":500,"currency":"USD","period_start":1782997200000,"period_end":1785675600000},"current_period_start_date":"2026-07-02T09:00:00.000Z","current_period_end_date":"2026-08-02T09:00:00.000Z"}}`,
			wantType:    domain.PaymentEventTypeRenewalFailed,
			wantOrderNo: "RNL-FAILED-1",
			wantTradeNo: "ord_renewal_failed_1",
			wantAmount:  500,
			wantPeriod:  true,
		},
		{
			name:        "subscription expired",
			payload:     `{"id":"evt_expired_1","eventType":"subscription.expired","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"RNL-EXPIRED-1"}},"order":{"id":"ord_expired_1","amount":500,"currency":"USD","period_start":1782997200000,"period_end":1785675600000},"current_period_start_date":"2026-07-02T09:00:00.000Z","current_period_end_date":"2026-08-02T09:00:00.000Z"}}`,
			wantType:    domain.PaymentEventTypeSubscriptionExpired,
			wantOrderNo: "RNL-EXPIRED-1",
			wantTradeNo: "ord_expired_1",
			wantAmount:  500,
			wantPeriod:  true,
		},
		{
			name:        "subscription cancel",
			payload:     `{"id":"evt_cancel_1","eventType":"subscription.cancelled","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"RNL-CANCEL-1"}},"order":{"id":"ord_cancel_1","amount":500,"currency":"USD"}}}`,
			wantType:    domain.PaymentEventTypeCancelled,
			wantOrderNo: "RNL-CANCEL-1",
			wantTradeNo: "ord_cancel_1",
			wantAmount:  500,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			payload := []byte(fixture.payload)
			event, err := adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
				Headers:    map[string]string{"creem-signature": testCreemSignature(payload, "whsec_test")},
				RawPayload: payload,
			})
			if err != nil {
				t.Fatalf("verify webhook: %v", err)
			}
			if event.EventType != fixture.wantType || event.OutTradeNo != fixture.wantOrderNo || event.ProviderTradeNo != fixture.wantTradeNo || event.Amount != fixture.wantAmount || !event.SignatureVerified {
				t.Fatalf("unexpected normalized event: %#v", event)
			}
			if fixture.wantPeriod && (event.PeriodStartAt == nil || event.PeriodEndAt == nil || !event.PeriodEndAt.After(*event.PeriodStartAt)) {
				t.Fatalf("expected valid period projection, got start=%v end=%v", event.PeriodStartAt, event.PeriodEndAt)
			}
		})
	}
}
