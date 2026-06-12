package sdk_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"walnut-billing/sdk"
)

func Example_basicUsage() {
	// Create a new client with a license key
	client, err := sdk.NewClient("SM-PRO-0001-0001",
		sdk.WithBaseURL("http://localhost:8082"),
		sdk.WithOfflineGracePeriod(24*time.Hour),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Verify the license
	result, err := client.Verify(ctx)
	if err != nil {
		log.Fatalf("Verification failed: %v", err)
	}

	if !result.IsValid {
		fmt.Println("License is invalid or expired")
		return
	}

	if result.IsOffline {
		fmt.Println("Running in offline mode (server unreachable)")
	} else {
		fmt.Printf("License active, expires: %s\n", result.ExpiresAt)
	}
}

func Example_verifyAndActivate() {
	client, err := sdk.NewClient("SM-PRO-0001-0001")
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Verify and automatically activate if needed
	result, err := client.VerifyAndActivate(ctx)
	if err != nil {
		log.Fatalf("Verify and activate failed: %v", err)
	}

	fmt.Printf("Status: %s, Offline: %v\n", result.ServerStatus, result.IsOffline)
}
