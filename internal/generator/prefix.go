package generator

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const charset = "0123456789"

// PrefixKeyGenerator generates keys in the format: SM-{PREFIX}-{RANDOM}-{RANDOM}
// Example: SM-PRO-7738-9921
type PrefixKeyGenerator struct {
	Prefix string // e.g., "PRO", "STD", "SUB"
}

func NewPrefixKeyGenerator(prefix string) *PrefixKeyGenerator {
	return &PrefixKeyGenerator{Prefix: prefix}
}

func (g *PrefixKeyGenerator) Generate(productCode string) (string, error) {
	part1, err := randomDigits(4)
	if err != nil {
		return "", err
	}
	part2, err := randomDigits(4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SM-%s-%s-%s", strings.ToUpper(g.Prefix), part1, part2), nil
}

func randomDigits(n int) (string, error) {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		sb.WriteByte(charset[num.Int64()])
	}
	return sb.String(), nil
}
