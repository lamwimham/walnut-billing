package payment

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WechatPayV3Adapter implements PaymentProvider for WeChat Pay V3 (Native).
// Uses official RSA signing + AES-256-GCM decryption.
type WechatPayV3Adapter struct {
	AppID       string         // 公众号/小程序 AppID
	MchID       string         // 商户号
	SerialNo    string         // 商户证书序列号
	PrivateKey  *rsa.PrivateKey // 商户私钥
	APIv3Key    string         // APIv3 密钥（用于解密回调）
	NotifyURL   string         // 回调地址
	SandboxMode bool           // If true, use sandbox base URL
	HTTPClient  *http.Client
	apiBaseURL  string
}

// WechatPayV3Config holds the configuration for WeChat Pay V3.
type WechatPayV3Config struct {
	AppID       string
	MchID       string
	SerialNo    string
	PrivateKey  string // PEM encoded private key
	APIv3Key    string
	NotifyURL   string
	SandboxMode bool   // If true, use sandbox environment
}

// Validate checks if the required credentials are present.
func (cfg WechatPayV3Config) Validate() error {
	required := map[string]string{
		"app_id":      cfg.AppID,
		"mch_id":      cfg.MchID,
		"serial_no":   cfg.SerialNo,
		"private_key": cfg.PrivateKey,
		"api_v3_key":  cfg.APIv3Key,
	}
	for name, val := range required {
		if val == "" {
			return fmt.Errorf("missing required WeChat Pay config: %s", name)
		}
	}
	return nil
}

// NewWechatPayV3Adapter creates a new WeChat Pay V3 adapter.
func NewWechatPayV3Adapter(cfg WechatPayV3Config) (*WechatPayV3Adapter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	privKey, err := parseRSAPrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	baseURL := "https://api.mch.weixin.qq.com"
	if cfg.SandboxMode {
		baseURL = "https://api.mch.weixin.qq.com/sandboxnew"
	}

	return &WechatPayV3Adapter{
		AppID:       cfg.AppID,
		MchID:       cfg.MchID,
		SerialNo:    cfg.SerialNo,
		PrivateKey:  privKey,
		APIv3Key:    cfg.APIv3Key,
		NotifyURL:   cfg.NotifyURL,
		SandboxMode: cfg.SandboxMode,
		apiBaseURL:  baseURL,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (w *WechatPayV3Adapter) Name() string {
	return "wechat"
}

// CreatePaymentURL calls WeChat Native Pay API to get code_url.
func (w *WechatPayV3Adapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	body := map[string]interface{}{
		"mchid":      w.MchID,
		"out_trade_no": outTradeNo,
		"appid":      w.AppID,
		"description": description,
		"notify_url": w.NotifyURL,
		"amount": map[string]interface{}{
			"total":    amount,
			"currency": "CNY",
		},
	}

	bodyBytes, _ := json.Marshal(body)

	timestamp := time.Now().Unix()
	nonce := randomString(32)
	urlPath := "/v3/pay/transactions/native"
	message := fmt.Sprintf("POST\n%s\n%d\n%s\n%s\n", urlPath, timestamp, nonce, string(bodyBytes))

	signature, err := signRSASHA256(message, w.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign message: %w", err)
	}

	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%d",serial_no="%s"`,
		w.MchID, nonce, signature, timestamp, w.SerialNo)

	req, err := http.NewRequestWithContext(ctx, "POST", w.apiBaseURL+urlPath, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wechat API error: %d %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if result.CodeURL == "" {
		return "", fmt.Errorf("empty code_url from WeChat API")
	}

	return result.CodeURL, nil
}

// VerifyCallback validates and decrypts the WeChat Pay V3 callback.
func (w *WechatPayV3Adapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	body := params["body"]
	if body == "" {
		return "", "", 0, fmt.Errorf("empty callback body")
	}

	var callback struct {
		ID         string `json:"id"`
		CreateTime string `json:"create_time"`
		EventType  string `json:"event_type"`
		Summary    string `json:"summary"`
		Resource   struct {
			OriginalType   string `json:"original_type"`
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			AssociatedData string `json:"associated_data"`
			Nonce          string `json:"nonce"`
		} `json:"resource"`
	}
	if err := json.Unmarshal([]byte(body), &callback); err != nil {
		return "", "", 0, fmt.Errorf("parse callback: %w", err)
	}

	decrypted, err := decryptAES256GCM(
		callback.Resource.Ciphertext,
		callback.Resource.AssociatedData,
		callback.Resource.Nonce,
		w.APIv3Key,
	)
	if err != nil {
		return "", "", 0, fmt.Errorf("decrypt callback: %w", err)
	}

	var decryptedData struct {
		OutTradeNo string `json:"out_trade_no"`
		TradeNo    string `json:"transaction_id"`
		TradeState string `json:"trade_state"`
		Amount     struct {
			Total int64 `json:"total"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(decrypted, &decryptedData); err != nil {
		return "", "", 0, fmt.Errorf("parse decrypted: %w", err)
	}

	if decryptedData.TradeState != "SUCCESS" {
		return "", "", 0, fmt.Errorf("trade state: %s", decryptedData.TradeState)
	}

	return decryptedData.OutTradeNo, decryptedData.TradeNo, decryptedData.Amount.Total, nil
}

func (w *WechatPayV3Adapter) BuildSuccessResponse() (contentType string, body string) {
	return "application/json", `{"code":"SUCCESS","message":"成功"}`
}

func (w *WechatPayV3Adapter) BuildFailureResponse() (contentType string, body string) {
	return "application/json", `{"code":"FAIL","message":"失败"}`
}

// decryptAES256GCM decrypts WeChat Pay V3 callback body.
func decryptAES256GCM(ciphertextB64, associatedData, nonce, key string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, []byte(nonce), ciphertext, []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

func randomString(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[time.Now().UnixNano()%int64(len(chars))]
	}
	return string(b)
}
