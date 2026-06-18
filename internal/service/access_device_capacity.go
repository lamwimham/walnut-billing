package service

const defaultAccessMaxDevices = 2

// AccessDeviceCapacity is the machine-readable device-slot projection returned
// by access-session APIs and embedded in signed access snapshots.
type AccessDeviceCapacity struct {
	ActiveDeviceCount    int `json:"active_device_count"`
	MaxDevices           int `json:"max_devices"`
	RemainingDeviceSlots int `json:"remaining_device_slots"`
}

func newAccessDeviceCapacity(activeDeviceCount int, maxDevices int) AccessDeviceCapacity {
	if activeDeviceCount < 0 {
		activeDeviceCount = 0
	}
	maxDevices = normalizeAccessMaxDevices(maxDevices)
	remaining := maxDevices - activeDeviceCount
	if remaining < 0 {
		remaining = 0
	}
	return AccessDeviceCapacity{
		ActiveDeviceCount:    activeDeviceCount,
		MaxDevices:           maxDevices,
		RemainingDeviceSlots: remaining,
	}
}

func normalizeAccessMaxDevices(maxDevices int) int {
	if maxDevices <= 0 {
		return defaultAccessMaxDevices
	}
	return maxDevices
}
