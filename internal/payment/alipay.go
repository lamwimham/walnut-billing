package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AlipayV2Adapter implements PaymentProvider for Alipay (Open API V2).
// Uses RSA2 (SHA256WithRSA) for signing and verification.
type AlipayV2Adapter struct {
	AppID       string
	PrivateKey  *rsa.PrivateKey
	PublicKey   *rsa.PublicKey // Alipay public key (for callback verification)
	NotifyURL   string
	SandboxMode bool
	HTTPClient  *http.Client
	gatewayURL  string
}

// AlipayV2Config holds the configuration for Alipay.
type AlipayV2Config struct {
	AppID       string
	PrivateKey  string // PEM encoded
	PublicKey   string // Alipay public key (PEM encoded, for callback verification)
	NotifyURL   string
	SandboxMode bool   // If true, use sandbox gateway
}

// Validate checks if the required credentials are present.
func (cfg AlipayV2Config) Validate() error {
	required := map[string]string{
		"app_id":      cfg.AppID,
		"private_key": cfg.PrivateKey,
	}
	for name, val := range required {
		if val == "" {
			return fmt.Errorf("missing required Alipay config: %s", name)
		}
	}
	return nil
}

// NewAlipayV2Adapter creates a new Alipay V2 adapter.
func NewAlipayV2Adapter(cfg AlipayV2Config) (*AlipayV2Adapter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	privKey, err := parseRSAPrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	var pubKey *rsa.PublicKey
	if cfg.PublicKey != "" {
		parsedKey, err := parseRSAPublicKey(cfg.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("parse alipay public key: %w", err)
		}
		pubKey = parsedKey
	}

	gatewayURL := "https://openapi.alipay.com/gateway.do"
	if cfg.SandboxMode {
		gatewayURL = "https://openapi-sandbox.dl.alipaydev.com/gateway.do"
	}

	return &AlipayV2Adapter{
		AppID:       cfg.AppID,
		PrivateKey:  privKey,
		PublicKey:   pubKey,
		NotifyURL:   cfg.NotifyURL,
		SandboxMode: cfg.SandboxMode,
		gatewayURL:  gatewayURL,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (a *AlipayV2Adapter) Name() string {
	return "alipay"
}

// CreatePaymentURL calls Alipay Trade Precreate API to get QR code URL.
func (a *AlipayV2Adapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	// Build request parameters
	params := url.Values{}
	params.Set("app_id", a.AppID)
	params.Set("method", "alipay.trade.precreate")
	params.Set("format", "JSON")
	params.Set("charset", "utf-8")
	params.Set("sign_type", "RSA2")
	params.Set("timestamp", time.Now().Format("2006-01-02 15:04:05"))
	params.Set("version", "1.0")
	params.Set("notify_url", a.NotifyURL)
	params.Set("biz_content", fmt.Sprintf(
		`{"out_trade_no":"%s","total_amount":"%.2f","subject":"%s"}`,
		outTradeNo,
		float64(amount)/100, // Convert cents to yuan
		description,
	))

	// Sign the parameters
	sign, err := a.signParams(params)
	if err != nil {
		return "", fmt.Errorf("sign params: %w", err)
	}
	params.Set("sign", sign)

	// Send request
	resp, err := a.HTTPClient.PostForm(a.gatewayURL, params)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Parse response
	var result struct {
		AlipayTradePrecreateResponse struct {
			Code      string `json:"code"`
			Msg       string `json:"msg"`
			OutTradeNo string `json:"out_trade_no"`
			QRCode    string `json:"qr_code"`
		} `json:"alipay_trade_precreate_response"`
		Sign string `json:"sign"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if result.AlipayTradePrecreateResponse.Code != "10000" {
		return "", fmt.Errorf("alipay API error: %s - %s",
			result.AlipayTradePrecreateResponse.Code,
			result.AlipayTradePrecreateResponse.Msg)
	}

	if result.AlipayTradePrecreateResponse.QRCode == "" {
		return "", fmt.Errorf("empty qr_code from Alipay API")
	}

	return result.AlipayTradePrecreateResponse.QRCode, nil
}

// VerifyCallback validates the Alipay callback signature.
func (a *AlipayV2Adapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	// Verify signature if public key is configured
	if a.PublicKey != nil {
		if err := a.verifyCallbackSignature(params); err != nil {
			return "", "", 0, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	outTradeNo = params["out_trade_no"]
	providerTradeNo = params["trade_no"]
	tradeStatus := params["trade_status"]

	if outTradeNo == "" {
		return "", "", 0, fmt.Errorf("missing out_trade_no in callback")
	}

	if tradeStatus != "TRADE_SUCCESS" && tradeStatus != "TRADE_FINISHED" {
		return "", "", 0, fmt.Errorf("trade status: %s", tradeStatus)
	}

	// Amount in yuan, convert to cents
	if totalAmount, ok := params["total_amount"]; ok {
		var yuan float64
		fmt.Sscanf(totalAmount, "%f", &yuan)
		amount = int64(yuan * 100)
	}

	return outTradeNo, providerTradeNo, amount, nil
}

func (a *AlipayV2Adapter) BuildSuccessResponse() (contentType string, body string) {
	return "text/plain", "success"
}

func (a *AlipayV2Adapter) BuildFailureResponse() (contentType string, body string) {
	return "text/plain", "fail"
}

// signParams creates RSA2 signature for Alipay request.
func (a *AlipayV2Adapter) signParams(params url.Values) (string, error) {
	// Sort parameters
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || params.Get(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build sign string
	var signStr strings.Builder
	for i, k := range keys {
		if i > 0 {
			signStr.WriteString("&")
		}
		signStr.WriteString(k)
		signStr.WriteString("=")
		signStr.WriteString(params.Get(k))
	}

	// SHA256WithRSA
	hash := sha256.New()
	hash.Write([]byte(signStr.String()))
	digest := hash.Sum(nil)

	signature, err := rsa.SignPKCS1v15(rand.Reader, a.PrivateKey, crypto.SHA256, digest)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// verifyCallbackSignature verifies the Alipay callback signature.
func (a *AlipayV2Adapter) verifyCallbackSignature(params map[string]string) error {
	signB64 := params["sign"]
	if signB64 == "" {
		return fmt.Errorf("missing sign in callback")
	}

	signature, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	// Build sign string (same as signing)
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var signStr strings.Builder
	for i, k := range keys {
		if i > 0 {
			signStr.WriteString("&")
		}
		signStr.WriteString(k)
		signStr.WriteString("=")
		signStr.WriteString(params[k])
	}

	hash := sha256.New()
	hash.Write([]byte(signStr.String()))
	digest := hash.Sum(nil)

	return rsa.VerifyPKCS1v15(a.PublicKey, crypto.SHA256, digest, signature)
}


