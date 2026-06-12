package apierror

import (
	"net/http"
)

// ErrorCode represents a business-level error code.
type ErrorCode string

const (
	ErrInternal         ErrorCode = "INTERNAL_ERROR"
	ErrBadRequest       ErrorCode = "BAD_REQUEST"
	ErrUnauthorized     ErrorCode = "UNAUTHORIZED"
	ErrForbidden        ErrorCode = "FORBIDDEN"
	ErrNotFound         ErrorCode = "NOT_FOUND"
	ErrConflict         ErrorCode = "CONFLICT"
	ErrTooManyRequests  ErrorCode = "TOO_MANY_REQUESTS"

	// Business errors
	ErrLicenseNotFound  ErrorCode = "LICENSE_NOT_FOUND"
	ErrLicenseInactive  ErrorCode = "LICENSE_INACTIVE"
	ErrLicenseExpired   ErrorCode = "LICENSE_EXPIRED"
	ErrDeviceBound      ErrorCode = "DEVICE_BOUND"
	ErrProductNotFound  ErrorCode = "PRODUCT_NOT_FOUND"
	ErrOrderNotFound    ErrorCode = "ORDER_NOT_FOUND"
	ErrPaymentFailed    ErrorCode = "PAYMENT_FAILED"
)

// APIError is the unified error response format.
type APIError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details string    `json:"details,omitempty"` // Optional debug info (only in dev)
}

// HTTPStatus maps ErrorCode to HTTP status code.
func (e ErrorCode) HTTPStatus() int {
	switch e {
	case ErrInternal:
		return http.StatusInternalServerError
	case ErrBadRequest:
		return http.StatusBadRequest
	case ErrUnauthorized:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrNotFound, ErrLicenseNotFound, ErrProductNotFound, ErrOrderNotFound:
		return http.StatusNotFound
	case ErrConflict, ErrDeviceBound:
		return http.StatusConflict
	case ErrTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// New creates a new APIError.
func New(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// Newf creates a new APIError with formatted message.
func Newf(code ErrorCode, format string, args ...interface{}) *APIError {
	return &APIError{
		Code:    code,
		Message: format,
	}
}

// WithDetails adds optional debug details.
func (e *APIError) WithDetails(details string) *APIError {
	e.Details = details
	return e
}

// Respond writes the APIError to the response.
func Respond(w http.ResponseWriter, err *APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Code.HTTPStatus())

	resp := map[string]interface{}{
		"error": err.Code,
		"message": err.Message,
	}
	if err.Details != "" {
		resp["details"] = err.Details
	}

	// Marshal manually to avoid import cycle
	body := `{"error":"` + string(err.Code) + `","message":"` + err.Message + `"}`
	if err.Details != "" {
		body = `{"error":"` + string(err.Code) + `","message":"` + err.Message + `","details":"` + err.Details + `"}`
	}
	w.Write([]byte(body))
}
