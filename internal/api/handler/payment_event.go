package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type PaymentEventHandler struct {
	PaymentEventSvc service.PaymentEventService
}

func NewPaymentEventHandler(paymentEventSvc service.PaymentEventService) *PaymentEventHandler {
	return &PaymentEventHandler{PaymentEventSvc: paymentEventSvc}
}

// ReceiveWebhook handles provider webhook events through the inbox boundary.
// The handler only normalizes transport data; signature checks, idempotency, and
// processing are delegated to PaymentEventService and provider adapters.
func (h *PaymentEventHandler) ReceiveWebhook(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}
	if h.PaymentEventSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment event service unavailable"})
		return
	}

	rawPayload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	params := collectWebhookParams(c, rawPayload)
	result, err := h.PaymentEventSvc.ReceiveWebhook(c.Request.Context(), service.PaymentWebhookInput{
		Provider:   provider,
		Headers:    collectHeaders(c),
		Params:     params,
		RawPayload: rawPayload,
	})
	if err != nil {
		c.JSON(paymentEventErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(paymentEventSuccessStatus(result), paymentEventResultResponse(result))
}

func (h *PaymentEventHandler) ListEvents(c *gin.Context) {
	if h.PaymentEventSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment event service unavailable"})
		return
	}
	events, err := h.PaymentEventSvc.ListEvents(c.Request.Context(), repository.PaymentEventQuery{
		Provider:   c.Query("provider"),
		Status:     c.Query("status"),
		EventType:  c.Query("event_type"),
		OutTradeNo: c.Query("out_trade_no"),
		Limit:      parseQueryInt(c, "limit"),
		Offset:     parseQueryInt(c, "offset"),
	})
	if err != nil {
		c.JSON(paymentEventErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *PaymentEventHandler) GetEvent(c *gin.Context) {
	if h.PaymentEventSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment event service unavailable"})
		return
	}
	event, err := h.PaymentEventSvc.GetEvent(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(paymentEventErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"event": event})
}

func (h *PaymentEventHandler) ReprocessEvent(c *gin.Context) {
	if h.PaymentEventSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment event service unavailable"})
		return
	}
	result, err := h.PaymentEventSvc.Process(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(paymentEventErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(paymentEventSuccessStatus(result), paymentEventResultResponse(result))
}

func collectHeaders(c *gin.Context) map[string]string {
	headers := make(map[string]string)
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}

func collectWebhookParams(c *gin.Context, rawPayload []byte) map[string]string {
	params := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	if strings.Contains(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") {
		if values, err := url.ParseQuery(string(rawPayload)); err == nil {
			for key, formValues := range values {
				if len(formValues) > 0 {
					params[key] = formValues[0]
				}
			}
		}
	}
	var jsonPayload map[string]any
	if len(rawPayload) > 0 && json.Unmarshal(rawPayload, &jsonPayload) == nil {
		flattenWebhookJSON(params, "", jsonPayload)
	}
	return params
}

func flattenWebhookJSON(params map[string]string, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			flattenWebhookJSON(params, key, nested)
			if prefix != "" {
				flattenWebhookJSON(params, prefix+"."+key, nested)
			}
		}
	case string:
		params[prefix] = typed
	case float64:
		params[prefix] = strconv.FormatInt(int64(typed), 10)
	case bool:
		params[prefix] = strconv.FormatBool(typed)
	}
}

func parseQueryInt(c *gin.Context, key string) int {
	value := c.Query(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func paymentEventErrorStatus(err error) int {
	switch {
	case errors.Is(err, service.ErrInvalidPaymentEvent):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrPaymentEventNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrPaymentEventNotProcessable):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func paymentEventSuccessStatus(result *service.PaymentEventProcessResult) int {
	if result == nil || !result.Processed {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func paymentEventResultResponse(result *service.PaymentEventProcessResult) gin.H {
	if result == nil {
		return gin.H{}
	}
	return gin.H{
		"event":        paymentEventResponse(result.Event),
		"duplicate":    result.Duplicate,
		"processed":    result.Processed,
		"process_note": result.ProcessNote,
	}
}

func paymentEventResponse(event *domain.PaymentEventInbox) gin.H {
	if event == nil {
		return gin.H{}
	}
	return gin.H{
		"id":                 event.ID,
		"provider":           event.Provider,
		"provider_event_id":  event.ProviderEventID,
		"event_type":         event.EventType,
		"out_trade_no":       event.OutTradeNo,
		"provider_trade_no":  event.ProviderTradeNo,
		"amount":             event.Amount,
		"currency":           event.Currency,
		"signature_verified": event.SignatureVerified,
		"status":             event.Status,
		"attempts":           event.Attempts,
		"last_error":         event.LastError,
		"received_at":        event.ReceivedAt,
		"processed_at":       event.ProcessedAt,
	}
}
