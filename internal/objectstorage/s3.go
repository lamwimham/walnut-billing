package objectstorage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"walnut-billing/internal/service"
)

var (
	ErrInvalidS3Config   = errors.New("invalid s3 object storage config")
	ErrS3OperationFailed = errors.New("s3 object storage operation failed")
)

const (
	defaultProviderID       = "s3"
	defaultS3Service        = "s3"
	defaultTargetTTL        = 15 * time.Minute
	defaultOperationTTL     = time.Minute
	maxPresignTTLSeconds    = 7 * 24 * 60 * 60
	unsignedPayload         = "UNSIGNED-PAYLOAD"
	amzAlgorithm            = "AWS4-HMAC-SHA256"
	walnutContentHashHeader = "x-amz-meta-walnut-content-hash"
	walnutSizeBytesHeader   = "x-amz-meta-walnut-size-bytes"
	amzTaggingHeader        = "x-amz-tagging"
)

// S3CompatibleConfig contains the provider details needed to generate SigV4
// presigned targets without leaking storage credentials outside the server.
type S3CompatibleConfig struct {
	ProviderID        string
	EndpointURL       string
	Region            string
	Bucket            string
	AccessKeyID       string
	SecretAccessKey   string
	SessionToken      string
	ForcePathStyle    bool
	ObjectTagging     bool
	UploadTargetTTL   time.Duration
	DownloadTargetTTL time.Duration
	OperationTTL      time.Duration
	HTTPClient        *http.Client
	Now               func() time.Time
}

type S3CompatibleProvider struct {
	providerID        string
	endpoint          *url.URL
	region            string
	bucket            string
	accessKeyID       string
	secretAccessKey   string
	sessionToken      string
	forcePathStyle    bool
	objectTagging     bool
	uploadTargetTTL   time.Duration
	downloadTargetTTL time.Duration
	operationTTL      time.Duration
	httpClient        *http.Client
	now               func() time.Time
}

type presignInput struct {
	method     string
	objectKey  string
	headers    map[string]string
	expires    time.Duration
	requestNow time.Time
}

type presignResult struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

func NewS3CompatibleProvider(config S3CompatibleConfig) (*S3CompatibleProvider, error) {
	endpoint, err := parseS3Endpoint(config.EndpointURL)
	if err != nil {
		return nil, err
	}
	providerID := strings.TrimSpace(config.ProviderID)
	if providerID == "" {
		providerID = defaultProviderID
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		return nil, fmt.Errorf("%w: region is required", ErrInvalidS3Config)
	}
	bucket := strings.TrimSpace(config.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("%w: bucket is required", ErrInvalidS3Config)
	}
	accessKeyID := strings.TrimSpace(config.AccessKeyID)
	secretAccessKey := strings.TrimSpace(config.SecretAccessKey)
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("%w: access key id and secret access key are required", ErrInvalidS3Config)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &S3CompatibleProvider{
		providerID:        providerID,
		endpoint:          endpoint,
		region:            region,
		bucket:            bucket,
		accessKeyID:       accessKeyID,
		secretAccessKey:   secretAccessKey,
		sessionToken:      strings.TrimSpace(config.SessionToken),
		forcePathStyle:    config.ForcePathStyle,
		objectTagging:     config.ObjectTagging,
		uploadTargetTTL:   normalizeTTL(config.UploadTargetTTL, defaultTargetTTL),
		downloadTargetTTL: normalizeTTL(config.DownloadTargetTTL, defaultTargetTTL),
		operationTTL:      normalizeTTL(config.OperationTTL, defaultOperationTTL),
		httpClient:        client,
		now:               now,
	}, nil
}

func (p *S3CompatibleProvider) ProviderID() string {
	if p == nil || strings.TrimSpace(p.providerID) == "" {
		return defaultProviderID
	}
	return p.providerID
}

func (p *S3CompatibleProvider) BuildUploadTarget(ctx context.Context, request service.CloudObjectUploadRequest) (service.CloudObjectUploadTarget, error) {
	if err := ctx.Err(); err != nil {
		return service.CloudObjectUploadTarget{}, err
	}
	objectKey := strings.TrimSpace(request.ObjectKey)
	if p == nil || objectKey == "" || request.SizeBytes < 0 {
		return service.CloudObjectUploadTarget{}, ErrInvalidS3Config
	}
	headers := map[string]string{}
	if contentType := strings.TrimSpace(request.ContentType); contentType != "" {
		headers["Content-Type"] = contentType
	}
	if contentHash := strings.TrimSpace(request.ContentHash); contentHash != "" {
		headers[walnutContentHashHeader] = contentHash
	}
	headers[walnutSizeBytesHeader] = strconv.FormatInt(request.SizeBytes, 10)
	if p.objectTagging {
		tagHeader := encodeLifecycleTags(request.LifecycleTags)
		if tagHeader != "" {
			headers[amzTaggingHeader] = tagHeader
		}
	}
	presigned, err := p.presign(presignInput{method: http.MethodPut, objectKey: objectKey, headers: headers, expires: p.uploadTargetTTL})
	if err != nil {
		return service.CloudObjectUploadTarget{}, err
	}
	return service.CloudObjectUploadTarget{
		ObjectKey: objectKey,
		UploadURL: presigned.URL,
		Method:    http.MethodPut,
		Headers:   presigned.Headers,
		Provider:  p.ProviderID(),
	}, nil
}

func (p *S3CompatibleProvider) BuildDownloadTarget(ctx context.Context, request service.CloudObjectDownloadRequest) (service.CloudObjectDownloadTarget, error) {
	if err := ctx.Err(); err != nil {
		return service.CloudObjectDownloadTarget{}, err
	}
	objectKey := strings.TrimSpace(request.ObjectKey)
	if p == nil || objectKey == "" {
		return service.CloudObjectDownloadTarget{}, ErrInvalidS3Config
	}
	presigned, err := p.presign(presignInput{method: http.MethodGet, objectKey: objectKey, expires: p.downloadTargetTTL})
	if err != nil {
		return service.CloudObjectDownloadTarget{}, err
	}
	return service.CloudObjectDownloadTarget{
		ObjectKey:   objectKey,
		DownloadURL: presigned.URL,
		Method:      http.MethodGet,
		Headers:     presigned.Headers,
		Provider:    p.ProviderID(),
		ExpiresAt:   presigned.ExpiresAt,
	}, nil
}

func (p *S3CompatibleProvider) DeleteObject(ctx context.Context, request service.CloudObjectDeleteRequest) error {
	objectKey := strings.TrimSpace(request.ObjectKey)
	if p == nil || objectKey == "" {
		return ErrInvalidS3Config
	}
	presigned, err := p.presign(presignInput{method: http.MethodDelete, objectKey: objectKey, expires: p.operationTTL})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, presigned.URL, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: delete object: %v", ErrS3OperationFailed, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	return fmt.Errorf("%w: delete object status %d", ErrS3OperationFailed, resp.StatusCode)
}

func (p *S3CompatibleProvider) HeadObject(ctx context.Context, request service.CloudObjectHeadRequest) (service.CloudObjectHeadResult, error) {
	objectKey := strings.TrimSpace(request.ObjectKey)
	if p == nil || objectKey == "" {
		return service.CloudObjectHeadResult{}, ErrInvalidS3Config
	}
	presigned, err := p.presign(presignInput{method: http.MethodHead, objectKey: objectKey, expires: p.operationTTL})
	if err != nil {
		return service.CloudObjectHeadResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, presigned.URL, nil)
	if err != nil {
		return service.CloudObjectHeadResult{}, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return service.CloudObjectHeadResult{}, fmt.Errorf("%w: head object: %v", ErrS3OperationFailed, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return service.CloudObjectHeadResult{ObjectKey: objectKey, Exists: false, Provider: p.ProviderID()}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return service.CloudObjectHeadResult{}, fmt.Errorf("%w: head object status %d", ErrS3OperationFailed, resp.StatusCode)
	}
	return service.CloudObjectHeadResult{
		ObjectKey:   objectKey,
		Exists:      true,
		SizeBytes:   resp.ContentLength,
		ContentHash: resp.Header.Get(walnutContentHashHeader),
		ContentType: resp.Header.Get("Content-Type"),
		Provider:    p.ProviderID(),
	}, nil
}

func (p *S3CompatibleProvider) presign(input presignInput) (presignResult, error) {
	if p == nil || strings.TrimSpace(input.method) == "" || strings.TrimSpace(input.objectKey) == "" {
		return presignResult{}, ErrInvalidS3Config
	}
	now := input.requestNow.UTC()
	if now.IsZero() {
		now = p.now().UTC()
	}
	expires := normalizeTTL(input.expires, defaultTargetTTL)
	expiresSeconds := int(expires.Seconds())
	if expiresSeconds > maxPresignTTLSeconds {
		expiresSeconds = maxPresignTTLSeconds
		expires = time.Duration(expiresSeconds) * time.Second
	}
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	host, canonicalURI := p.objectHostAndPath(input.objectKey)
	headers := canonicalHeaderMap(host, input.headers)
	signedHeaders := signedHeaderNames(headers)
	scope := fmt.Sprintf("%s/%s/%s/aws4_request", date, p.region, defaultS3Service)
	query := map[string]string{
		"X-Amz-Algorithm":     amzAlgorithm,
		"X-Amz-Credential":    p.accessKeyID + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       strconv.Itoa(expiresSeconds),
		"X-Amz-SignedHeaders": signedHeaders,
	}
	if p.sessionToken != "" {
		query["X-Amz-Security-Token"] = p.sessionToken
	}
	canonicalQuery := canonicalQueryString(query)
	canonicalHeaders := canonicalHeadersString(headers)
	canonicalRequest := strings.Join([]string{
		strings.ToUpper(input.method),
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		unsignedPayload,
	}, "\n")
	stringToSign := strings.Join([]string{
		amzAlgorithm,
		amzDate,
		scope,
		sha256Hex(canonicalRequest),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(p.secretAccessKey, date, p.region, defaultS3Service), stringToSign))
	urlString := p.endpoint.Scheme + "://" + host + canonicalURI + "?" + canonicalQuery + "&X-Amz-Signature=" + signature
	return presignResult{URL: urlString, Headers: cloneHeaders(input.headers), ExpiresAt: now.Add(expires)}, nil
}

func (p *S3CompatibleProvider) objectHostAndPath(objectKey string) (string, string) {
	endpointHost := p.endpoint.Host
	if p.forcePathStyle {
		return endpointHost, "/" + awsURIEncode(p.bucket, true) + "/" + awsURIEncode(objectKey, false)
	}
	return p.bucket + "." + endpointHost, "/" + awsURIEncode(objectKey, false)
}

func parseS3Endpoint(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("%w: endpoint URL is required", ErrInvalidS3Config)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: endpoint URL must include scheme and host", ErrInvalidS3Config)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%w: endpoint URL must be http or https", ErrInvalidS3Config)
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint URL must not include path, query, or fragment", ErrInvalidS3Config)
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeTTL(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value < time.Second {
		return time.Second
	}
	max := time.Duration(maxPresignTTLSeconds) * time.Second
	if value > max {
		return max
	}
	return value
}

func canonicalHeaderMap(host string, headers map[string]string) map[string]string {
	canonical := map[string]string{"host": host}
	for name, value := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		value = normalizeHeaderValue(value)
		if name == "" || value == "" {
			continue
		}
		canonical[name] = value
	}
	return canonical
}

func signedHeaderNames(headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ";")
}

func canonicalHeadersString(headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(headers[name])
		b.WriteByte('\n')
	}
	return b.String()
}

func normalizeHeaderValue(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func canonicalQueryString(values map[string]string) string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, awsURIEncode(key, true)+"="+awsURIEncode(value, true))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	clone := make(map[string]string, len(headers))
	for key, value := range headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		clone[key] = value
	}
	return clone
}

func encodeLifecycleTags(tags []service.CloudObjectLifecycleTag) string {
	if len(tags) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(tags))
	seen := map[string]string{}
	for _, tag := range tags {
		key := strings.TrimSpace(tag.Key)
		value := strings.TrimSpace(tag.Value)
		if key == "" || value == "" {
			continue
		}
		seen[key] = value
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		pairs = append(pairs, url.QueryEscape(key)+"="+url.QueryEscape(seen[key]))
	}
	return strings.Join(pairs, "&")
}

func awsURIEncode(value string, encodeSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(value) {
		if isAWSUnreserved(c) || c == '/' && !encodeSlash {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
	}
	return b.String()
}

func isAWSUnreserved(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func signingKey(secret string, date string, region string, serviceName string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), date)
	dateRegionKey := hmacSHA256(dateKey, region)
	dateRegionServiceKey := hmacSHA256(dateRegionKey, serviceName)
	return hmacSHA256(dateRegionServiceKey, "aws4_request")
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var _ service.ObjectStorageProvider = (*S3CompatibleProvider)(nil)
