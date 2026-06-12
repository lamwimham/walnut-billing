package generator

// KeyGenerator defines the interface for license key generation.
// Different products can use different generation strategies.
type KeyGenerator interface {
	// Generate produces a new license key for the given product code.
	Generate(productCode string) (string, error)
}
