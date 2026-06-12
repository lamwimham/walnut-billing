package generator

import (
	"regexp"
	"testing"
)

func TestPrefixKeyGenerator_Generate(t *testing.T) {
	gen := NewPrefixKeyGenerator("PRO")

	key, err := gen.Generate("pro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected format: SM-PRO-XXXX-XXXX (4 digits each)
	pattern := regexp.MustCompile(`^SM-PRO-\d{4}-\d{4}$`)
	if !pattern.MatchString(key) {
		t.Errorf("key %q doesn't match expected format SM-PRO-XXXX-XXXX", key)
	}
}

func TestPrefixKeyGenerator_Generate_Unique(t *testing.T) {
	gen := NewPrefixKeyGenerator("STD")

	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key, err := gen.Generate("std")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if keys[key] {
			t.Errorf("duplicate key generated: %s", key)
		}
		keys[key] = true
	}
}

func TestKeyGeneratorFactory_Get(t *testing.T) {
	factory := NewKeyGeneratorFactory()
	factory.Register("pro", NewPrefixKeyGenerator("PRO"))

	gen, err := factory.Get("pro")
	if err != nil {
		t.Fatalf("expected generator for 'pro', got error: %v", err)
	}
	if gen == nil {
		t.Fatal("expected non-nil generator")
	}

	// Test fallback for unknown product code
	gen2, err := factory.Get("unknown")
	if err != nil {
		t.Fatalf("expected fallback generator, got error: %v", err)
	}
	if gen2 == nil {
		t.Fatal("expected non-nil fallback generator")
	}
}

func TestDefaultFactory(t *testing.T) {
	factory := DefaultFactory()

	codes := []string{"pro", "std", "sub_monthly", "sub_yearly"}
	for _, code := range codes {
		gen, err := factory.Get(code)
		if err != nil {
			t.Errorf("failed to get generator for %q: %v", code, err)
			continue
		}
		key, err := gen.Generate(code)
		if err != nil {
			t.Errorf("failed to generate key for %q: %v", code, err)
			continue
		}
		if key == "" {
			t.Errorf("empty key for %q", code)
		}
	}
}

func TestValidateProductCode(t *testing.T) {
	if err := ValidateProductCode(""); err == nil {
		t.Error("expected error for empty product code")
	}
	if err := ValidateProductCode("pro"); err != nil {
		t.Errorf("expected no error for 'pro', got: %v", err)
	}
}
