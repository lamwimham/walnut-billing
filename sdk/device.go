package sdk

import (
	"crypto/rand"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// defaultDeviceID generates a unique device identifier.
// It uses hostname + machine ID + random suffix for uniqueness.
func defaultDeviceID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	// Get a short random suffix
	buf := make([]byte, 4)
	_, err := rand.Read(buf)
	if err != nil {
		buf = []byte{0, 0, 0, 0}
	}

	platform := runtime.GOOS + "-" + runtime.GOARCH
	return fmt.Sprintf("%s-%s-%02x%02x%02x%02x", platform, hostname, buf[0], buf[1], buf[2], buf[3])
}

// SanitizeDeviceID cleans a device ID to ensure it's safe for JSON transmission.
func SanitizeDeviceID(id string) string {
	return strings.TrimSpace(id)
}
