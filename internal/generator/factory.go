package generator

import "fmt"

// KeyGeneratorFactory selects the appropriate KeyGenerator based on product code.
// This is the Factory Pattern — different products get different key formats.
type KeyGeneratorFactory struct {
	generators map[string]KeyGenerator
}

func NewKeyGeneratorFactory() *KeyGeneratorFactory {
	return &KeyGeneratorFactory{
		generators: make(map[string]KeyGenerator),
	}
}

// Register binds a generator to a product code.
func (f *KeyGeneratorFactory) Register(productCode string, gen KeyGenerator) {
	f.generators[productCode] = gen
}

// Get returns the generator for a product code, or a default prefix generator.
func (f *KeyGeneratorFactory) Get(productCode string) (KeyGenerator, error) {
	if gen, ok := f.generators[productCode]; ok {
		return gen, nil
	}
	// Fallback: use the product code itself as the prefix
	return NewPrefixKeyGenerator(productCode), nil
}

// RegisterDefaults registers the standard walnut product generators.
func RegisterDefaults(factory *KeyGeneratorFactory) {
	factory.Register("pro", NewPrefixKeyGenerator("PRO"))
	factory.Register("std", NewPrefixKeyGenerator("STD"))
	factory.Register("sub_monthly", NewPrefixKeyGenerator("SUB"))
	factory.Register("sub_yearly", NewPrefixKeyGenerator("SUB"))
}

// DefaultFactory returns a factory pre-configured with walnut product generators.
func DefaultFactory() *KeyGeneratorFactory {
	f := NewKeyGeneratorFactory()
	RegisterDefaults(f)
	return f
}

// ValidateProductCode ensures the product code is valid.
func ValidateProductCode(code string) error {
	if code == "" {
		return fmt.Errorf("product code cannot be empty")
	}
	return nil
}
