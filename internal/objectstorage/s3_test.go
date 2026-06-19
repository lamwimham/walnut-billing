package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/service"
)

func testS3Provider(t *testing.T, opts ...func(*S3CompatibleConfig)) *S3CompatibleProvider {
	t.Helper()
	cfg := S3CompatibleConfig{
		ProviderID:        "r2",
		EndpointURL:       "https://account.r2.cloudflarestorage.com",
		Region:            "auto",
		Bucket:            "walnut-sync",
		AccessKeyID:       "AKIATEST",
		SecretAccessKey:   "secret",
		ObjectTagging:     true,
		UploadTargetTTL:   10 * time.Minute,
		DownloadTargetTTL: 5 * time.Minute,
		OperationTTL:      time.Minute,
		Now:               func() time.Time { return time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC) },
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	provider, err := NewS3CompatibleProvider(cfg)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return provider
}

func TestS3CompatibleProviderBuildsUploadAndDownloadTargets(t *testing.T) {
	provider := testS3Provider(t)
	ctx := context.Background()

	upload, err := provider.BuildUploadTarget(ctx, service.CloudObjectUploadRequest{
		ObjectKey:   "accounts/usr_1/projects/local/wiki/hash/page one.md",
		ContentType: "text/markdown",
		ContentHash: "sha256:page",
		SizeBytes:   123,
		LifecycleTags: []service.CloudObjectLifecycleTag{
			{Key: "walnut.user_id", Value: "usr_1"},
			{Key: "walnut.client_project_id", Value: "local"},
		},
	})
	if err != nil {
		t.Fatalf("build upload target: %v", err)
	}
	if upload.Provider != "r2" || upload.Method != http.MethodPut || upload.ObjectKey == "" {
		t.Fatalf("unexpected upload target: %#v", upload)
	}
	parsedUpload, err := url.Parse(upload.UploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	if parsedUpload.Host != "walnut-sync.account.r2.cloudflarestorage.com" {
		t.Fatalf("unexpected virtual-host style host: %s", parsedUpload.Host)
	}
	if !strings.Contains(parsedUpload.EscapedPath(), "page%20one.md") {
		t.Fatalf("expected AWS URI encoded object path, got %s", parsedUpload.EscapedPath())
	}
	query := parsedUpload.Query()
	if query.Get("X-Amz-Algorithm") != amzAlgorithm || query.Get("X-Amz-Expires") != "600" || query.Get("X-Amz-Signature") == "" {
		t.Fatalf("unexpected SigV4 query: %s", parsedUpload.RawQuery)
	}
	if !strings.Contains(query.Get("X-Amz-SignedHeaders"), "x-amz-tagging") || !strings.Contains(query.Get("X-Amz-SignedHeaders"), walnutContentHashHeader) {
		t.Fatalf("expected metadata/tagging headers to be signed, got %s", query.Get("X-Amz-SignedHeaders"))
	}
	if upload.Headers["Content-Type"] != "text/markdown" || upload.Headers[walnutContentHashHeader] != "sha256:page" || upload.Headers[walnutSizeBytesHeader] != "123" || upload.Headers[amzTaggingHeader] == "" {
		t.Fatalf("unexpected upload headers: %#v", upload.Headers)
	}

	download, err := provider.BuildDownloadTarget(ctx, service.CloudObjectDownloadRequest{ObjectKey: upload.ObjectKey})
	if err != nil {
		t.Fatalf("build download target: %v", err)
	}
	if download.Provider != "r2" || download.Method != http.MethodGet || download.ExpiresAt.Format(time.RFC3339) != "2026-06-19T10:05:00Z" {
		t.Fatalf("unexpected download target: %#v", download)
	}
	parsedDownload, err := url.Parse(download.DownloadURL)
	if err != nil {
		t.Fatalf("parse download url: %v", err)
	}
	if parsedDownload.Query().Get("X-Amz-SignedHeaders") != "host" {
		t.Fatalf("expected download to sign only host, got %s", parsedDownload.Query().Get("X-Amz-SignedHeaders"))
	}
}

func TestS3CompatibleProviderSupportsPathStyleAndTemporaryCredentials(t *testing.T) {
	provider := testS3Provider(t, func(cfg *S3CompatibleConfig) {
		cfg.EndpointURL = "https://s3.us-east-1.amazonaws.com"
		cfg.Region = "us-east-1"
		cfg.ProviderID = "s3"
		cfg.ForcePathStyle = true
		cfg.SessionToken = "temporary-session"
	})

	target, err := provider.BuildDownloadTarget(context.Background(), service.CloudObjectDownloadRequest{ObjectKey: "accounts/usr_1/file.txt"})
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	parsed, err := url.Parse(target.DownloadURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	if parsed.Host != "s3.us-east-1.amazonaws.com" || !strings.HasPrefix(parsed.EscapedPath(), "/walnut-sync/accounts/usr_1/") {
		t.Fatalf("unexpected path-style target: %s", target.DownloadURL)
	}
	if parsed.Query().Get("X-Amz-Security-Token") != "temporary-session" {
		t.Fatalf("expected security token in query, got %s", parsed.RawQuery)
	}
}

func TestS3CompatibleProviderHeadAndDeleteUseSignedRequests(t *testing.T) {
	var methods []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		if r.URL.Query().Get("X-Amz-Signature") == "" {
			t.Fatalf("expected signed request query, got %s", r.URL.RawQuery)
		}
		response := &http.Response{
			StatusCode: http.StatusMethodNotAllowed,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}
		switch r.Method {
		case http.MethodHead:
			response.StatusCode = http.StatusOK
			response.ContentLength = 42
			response.Header.Set("Content-Type", "text/plain")
			response.Header.Set(walnutContentHashHeader, "sha256:body")
		case http.MethodDelete:
			response.StatusCode = http.StatusNoContent
		}
		return response, nil
	})}

	provider := testS3Provider(t, func(cfg *S3CompatibleConfig) {
		cfg.EndpointURL = "https://storage.example.com"
		cfg.ForcePathStyle = true
		cfg.HTTPClient = client
	})
	ctx := context.Background()
	head, err := provider.HeadObject(ctx, service.CloudObjectHeadRequest{ObjectKey: "accounts/usr_1/file.txt"})
	if err != nil {
		t.Fatalf("head object: %v", err)
	}
	if !head.Exists || head.SizeBytes != 42 || head.ContentHash != "sha256:body" || head.ContentType != "text/plain" {
		t.Fatalf("unexpected head result: %#v", head)
	}
	if err := provider.DeleteObject(ctx, service.CloudObjectDeleteRequest{ObjectKey: "accounts/usr_1/file.txt"}); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if strings.Join(methods, ",") != "HEAD,DELETE" {
		t.Fatalf("unexpected methods: %v", methods)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestS3CompatibleProviderValidatesConfig(t *testing.T) {
	_, err := NewS3CompatibleProvider(S3CompatibleConfig{EndpointURL: "https://storage.example.com", Region: "auto", Bucket: "walnut"})
	if err == nil || !strings.Contains(err.Error(), "access key id") {
		t.Fatalf("expected credential validation error, got %v", err)
	}
	_, err = NewS3CompatibleProvider(S3CompatibleConfig{EndpointURL: "https://storage.example.com/path", Region: "auto", Bucket: "walnut", AccessKeyID: "key", SecretAccessKey: "secret"})
	if err == nil || !strings.Contains(err.Error(), "must not include path") {
		t.Fatalf("expected endpoint validation error, got %v", err)
	}
}

func TestS3CompatibleProviderOmitsTaggingWhenDisabled(t *testing.T) {
	provider := testS3Provider(t, func(cfg *S3CompatibleConfig) {
		cfg.ObjectTagging = false
	})
	target, err := provider.BuildUploadTarget(context.Background(), service.CloudObjectUploadRequest{
		ObjectKey:   "accounts/usr_1/file.txt",
		ContentType: "text/plain",
		SizeBytes:   1,
		LifecycleTags: []service.CloudObjectLifecycleTag{
			{Key: "walnut.user_id", Value: "usr_1"},
		},
	})
	if err != nil {
		t.Fatalf("build upload target: %v", err)
	}
	if _, ok := target.Headers[amzTaggingHeader]; ok {
		t.Fatalf("did not expect tagging header when disabled: %#v", target.Headers)
	}
	parsed, err := url.Parse(target.UploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	if strings.Contains(parsed.Query().Get("X-Amz-SignedHeaders"), "x-amz-tagging") {
		t.Fatalf("did not expect x-amz-tagging to be signed: %s", parsed.Query().Get("X-Amz-SignedHeaders"))
	}
}
