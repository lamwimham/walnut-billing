# walnut Billing SDK

A lightweight Go client library for the walnut Billing microservice.

## Features

- **Offline-Tolerant Verification**: Falls back to cached state when server is unreachable
- **Exponential Backoff Retry**: Handles transient network failures gracefully
- **Zero-Boilerplate Activation**: Single `VerifyAndActivate()` call handles the full flow
- **Thread-Safe**: Safe for concurrent use in multi-threaded desktop apps

## Installation

Add to your `go.mod`:

```go
require walnut-billing v0.4.0

replace walnut-billing => ./path/to/walnut-billing
```

Or use git submodule / go workspace for independent versioning.

## Quick Start

```go
import (
    "context"
    "walnut-billing/sdk"
)

client, _ := sdk.NewClient("SM-PRO-0001-0001",
    sdk.WithBaseURL("http://localhost:8082"),
    sdk.WithOfflineGracePeriod(24 * time.Hour),
)

// Verify license (auto-fallback to offline cache if server unreachable)
result, err := client.Verify(context.Background())
if err != nil {
    // Completely failed (no cache or grace expired)
    log.Fatal(err)
}

if !result.IsValid {
    // License invalid or expired
    return
}

if result.IsOffline {
    // Server unreachable, but within grace period
    log.Println("Running offline")
}
```

## VerifyAndActivate

```go
result, err := client.VerifyAndActivate(ctx)
// Automatically activates if license is valid but inactive
// Handles: verify -> activate -> re-verify in one call
```

## Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithBaseURL` | `http://localhost:8082` | Billing server URL |
| `WithDeviceID` | Auto-generated | Unique device identifier |
| `WithOfflineGracePeriod` | 24h | Cache validity when offline |
| `WithMaxRetries` | 3 | Network request retry count |
| `WithTimeout` | 10s | Per-request timeout |

## Architecture

```
Client
├── Config (Functional Options)
├── Retry (Exponential Backoff + Jitter)
├── Verify (Server -> Fallback -> Cache)
└── Activate (Server -> Retry)
```

## Offline Grace Period Flow

```
Verify()
  ├── Server responds -> cache result, return
  └── Server error
        ├── Cache exists + within grace -> return cached (IsOffline=true)
        └── No cache or grace expired -> return error
```
