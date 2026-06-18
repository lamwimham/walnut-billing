package handler

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type MockCheckoutHandler struct {
	PaymentEventSvc service.PaymentEventService
}

func NewMockCheckoutHandler(paymentEventSvc service.PaymentEventService) *MockCheckoutHandler {
	return &MockCheckoutHandler{PaymentEventSvc: paymentEventSvc}
}

type mockCheckoutPageData struct {
	OutTradeNo  string
	SKUCode     string
	UserID      string
	SuccessURL  string
	CancelURL   string
	Error       string
	FormAction  string
	SuccessHref template.URL
	CancelHref  template.URL
}

type mockCheckoutSuccessPageData struct {
	OutTradeNo  string
	SuccessURL  string
	SuccessHref template.URL
}

// Show renders a local hosted-checkout stand-in for development builds.
func (h *MockCheckoutHandler) Show(c *gin.Context) {
	data := mockCheckoutPageData{
		OutTradeNo: strings.TrimSpace(c.Param("out_trade_no")),
		SKUCode:    strings.TrimSpace(c.Query("sku_code")),
		UserID:     strings.TrimSpace(c.Query("user_id")),
		SuccessURL: strings.TrimSpace(c.Query("success_url")),
		CancelURL:  strings.TrimSpace(c.Query("cancel_url")),
	}
	if data.OutTradeNo == "" {
		c.String(http.StatusBadRequest, "missing checkout order")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderMockCheckoutPage(data)))
}

// Complete simulates a paid provider webhook and renders a visible local result page.
func (h *MockCheckoutHandler) Complete(c *gin.Context) {
	outTradeNo := strings.TrimSpace(c.Param("out_trade_no"))
	if outTradeNo == "" {
		c.String(http.StatusBadRequest, "missing checkout order")
		return
	}
	if h == nil || h.PaymentEventSvc == nil {
		c.String(http.StatusServiceUnavailable, "mock checkout service unavailable")
		return
	}
	params := map[string]string{
		"out_trade_no":      outTradeNo,
		"provider_event_id": "evt_paid_" + strings.ReplaceAll(outTradeNo, "-", "_"),
		"event_type":        "payment.paid",
		"subscription_id":   "sub_mock_" + strings.ReplaceAll(outTradeNo, "-", "_"),
		"transaction_id":    "txn_" + outTradeNo,
		"currency":          "USD",
	}
	result, err := h.PaymentEventSvc.ReceiveWebhook(c.Request.Context(), service.PaymentWebhookInput{
		Provider: "mock",
		Params:   params,
		RawPayload: []byte(url.Values{
			"out_trade_no":      {params["out_trade_no"]},
			"provider_event_id": {params["provider_event_id"]},
			"event_type":        {params["event_type"]},
			"subscription_id":   {params["subscription_id"]},
			"transaction_id":    {params["transaction_id"]},
			"currency":          {params["currency"]},
		}.Encode()),
	})
	if err != nil {
		c.Data(http.StatusBadGateway, "text/html; charset=utf-8", []byte(renderMockCheckoutPage(mockCheckoutPageData{
			OutTradeNo: outTradeNo,
			SKUCode:    c.Query("sku_code"),
			UserID:     c.Query("user_id"),
			SuccessURL: c.Query("success_url"),
			CancelURL:  c.Query("cancel_url"),
			Error:      err.Error(),
		})))
		return
	}
	if result == nil || !result.Processed {
		c.Data(http.StatusAccepted, "text/html; charset=utf-8", []byte(renderMockCheckoutPage(mockCheckoutPageData{
			OutTradeNo: outTradeNo,
			SKUCode:    c.Query("sku_code"),
			UserID:     c.Query("user_id"),
			SuccessURL: c.Query("success_url"),
			CancelURL:  c.Query("cancel_url"),
			Error:      "mock payment event was accepted but not processed yet",
		})))
		return
	}
	successURL := strings.TrimSpace(c.Query("success_url"))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderMockCheckoutSuccessPage(mockCheckoutSuccessPageData{
		OutTradeNo:  outTradeNo,
		SuccessURL:  successURL,
		SuccessHref: mockCheckoutSafeCallbackURL(successURL),
	})))
}

func renderMockCheckoutPage(data mockCheckoutPageData) string {
	data.FormAction = mockCheckoutCompleteAction(data)
	data.SuccessHref = mockCheckoutSafeCallbackURL(data.SuccessURL)
	data.CancelHref = mockCheckoutSafeCallbackURL(data.CancelURL)
	var b strings.Builder
	_ = mockCheckoutTemplate.Execute(&b, data)
	return b.String()
}

func mockCheckoutCompleteAction(data mockCheckoutPageData) string {
	path := "/checkout/" + url.PathEscape(strings.TrimSpace(data.OutTradeNo)) + "/complete"
	query := url.Values{}
	if successURL := strings.TrimSpace(data.SuccessURL); successURL != "" {
		query.Set("success_url", successURL)
	}
	if cancelURL := strings.TrimSpace(data.CancelURL); cancelURL != "" {
		query.Set("cancel_url", cancelURL)
	}
	if skuCode := strings.TrimSpace(data.SKUCode); skuCode != "" {
		query.Set("sku_code", skuCode)
	}
	if userID := strings.TrimSpace(data.UserID); userID != "" {
		query.Set("user_id", userID)
	}
	if encoded := query.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func renderMockCheckoutSuccessPage(data mockCheckoutSuccessPageData) string {
	var b strings.Builder
	_ = mockCheckoutSuccessTemplate.Execute(&b, data)
	return b.String()
}

func mockCheckoutSafeCallbackURL(raw string) template.URL {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if scheme == "walnut" || ((scheme == "http" || scheme == "https") && isMockCheckoutLocalHost(host)) {
		return template.URL(value)
	}
	return ""
}

func isMockCheckoutLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" || host == "::1"
}

var mockCheckoutTemplate = template.Must(template.New("mock-checkout").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Walnut Mock Checkout</title>
  <style>` + mockCheckoutCSS + `</style>
</head>
<body>
  <main>
    <section>
      <p class="eyebrow">Walnut Mock Checkout</p>
      <h1>Complete a local test payment</h1>
      <p>This page simulates a hosted checkout provider and sends a mock paid webhook back to walnut-billing.</p>
      {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
      <dl>
        <div><dt>Order</dt><dd><code>{{.OutTradeNo}}</code></dd></div>
        {{if .SKUCode}}<div><dt>SKU</dt><dd>{{.SKUCode}}</dd></div>{{end}}
        {{if .UserID}}<div><dt>User</dt><dd>{{.UserID}}</dd></div>{{end}}
      </dl>
      <form method="post" action="{{.FormAction}}">
        <button type="submit">Simulate payment success</button>
      </form>
      {{if .CancelHref}}<a class="secondary" href="{{.CancelHref}}">Cancel checkout</a>{{end}}
    </section>
  </main>
</body>
</html>`))

var mockCheckoutSuccessTemplate = template.Must(template.New("mock-checkout-success").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Walnut Mock Checkout</title>
  <style>` + mockCheckoutCSS + `</style>
</head>
<body>
  <main>
    <section>
      <p class="eyebrow">Walnut Mock Checkout</p>
      <h1>Payment completed</h1>
      <p>Order <code>{{.OutTradeNo}}</code> has been marked as paid and the mock webhook has been processed.</p>
      {{if .SuccessHref}}
        <p>Next, return to Walnut so the app can refresh the signed access snapshot.</p>
        <a class="primary" href="{{.SuccessHref}}">Return to Walnut</a>
      {{else}}
        <p>Return to Walnut manually and click Refresh access in Settings → Software Access.</p>
      {{end}}
      <dl>
        <div><dt>Status</dt><dd>Paid / webhook processed</dd></div>
        {{if .SuccessURL}}<div><dt>Callback</dt><dd><code>{{.SuccessURL}}</code></dd></div>{{end}}
      </dl>
    </section>
  </main>
</body>
</html>`))

const mockCheckoutCSS = `
:root { color-scheme: light; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f3efe6; color: #1d1a16; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: radial-gradient(circle at 20% 10%, #ffe4b8 0, transparent 34%), linear-gradient(135deg, #f8f1df, #e8efe3); }
main { width: min(720px, calc(100vw - 32px)); }
section { border: 1px solid rgba(29,26,22,.12); border-radius: 28px; background: rgba(255,255,255,.76); box-shadow: 0 24px 80px rgba(47,38,24,.15); padding: 40px; }
.eyebrow { margin: 0 0 12px; text-transform: uppercase; letter-spacing: .18em; font-size: 12px; font-weight: 700; color: #8d5c16; }
h1 { margin: 0; font-size: clamp(32px, 6vw, 56px); line-height: .96; letter-spacing: -.05em; }
p { max-width: 560px; line-height: 1.7; color: #5f574c; }
dl { display: grid; gap: 12px; margin: 28px 0; }
dl div { border: 1px solid rgba(29,26,22,.1); border-radius: 16px; padding: 12px 14px; background: rgba(255,255,255,.52); }
dt { font-size: 11px; text-transform: uppercase; letter-spacing: .14em; color: #8d8071; }
dd { margin: 4px 0 0; font-weight: 650; }
code { word-break: break-all; }
button, a.primary { border: 0; border-radius: 999px; background: #1d1a16; color: white; padding: 14px 22px; font-size: 15px; font-weight: 760; cursor: pointer; text-decoration: none; display: inline-block; }
button:hover, a.primary:hover { background: #3b2f22; }
a.secondary { display: inline-block; margin-top: 14px; color: #6f4f1d; text-decoration: none; }
.error { margin: 18px 0; border: 1px solid rgba(174, 48, 38, .2); border-radius: 14px; background: rgba(174, 48, 38, .08); color: #9d2d25; padding: 12px; }
`
