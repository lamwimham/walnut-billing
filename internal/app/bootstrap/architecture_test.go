package bootstrap

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

type importRule struct {
	name      string
	root      string
	files     func(string) bool
	forbidden []string
}

func TestArchitectureImportBoundaries(t *testing.T) {
	rules := []importRule{
		{
			name: "services do not depend on transport layer",
			root: "../../service",
			forbidden: []string{
				"walnut-billing/internal/api",
				"walnut-billing/internal/api/handler",
			},
		},
		{
			name: "access services do not depend on payment providers",
			root: "../../service",
			files: func(path string) bool {
				base := filepath.Base(path)
				return strings.HasPrefix(base, "access_") || strings.HasPrefix(base, "credit") || base == "entitlement.go"
			},
			forbidden: []string{"walnut-billing/internal/payment"},
		},
		{
			name: "cloud storage services do not depend on payment providers",
			root: "../../service",
			files: func(path string) bool {
				return strings.HasPrefix(filepath.Base(path), "cloud_storage")
			},
			forbidden: []string{"walnut-billing/internal/payment"},
		},
		{
			name:      "handlers do not bypass repositories",
			root:      "../../api/handler",
			forbidden: []string{"walnut-billing/internal/repository/gorm_repo"},
		},
		{
			name: "domain is dependency-free from application layers",
			root: "../../domain",
			forbidden: []string{
				"walnut-billing/internal/api",
				"walnut-billing/internal/service",
				"walnut-billing/internal/payment",
				"walnut-billing/internal/repository",
			},
		},
	}

	for _, rule := range rules {
		t.Run(rule.name, func(t *testing.T) {
			assertImportRule(t, rule)
		})
	}
}

func assertImportRule(t *testing.T, rule importRule) {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, rule.root, func(info fs.FileInfo) bool {
		name := info.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return false
		}
		if rule.files == nil {
			return true
		}
		return rule.files(filepath.Join(rule.root, name))
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", rule.root, err)
	}
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			for _, spec := range file.Imports {
				importPath := strings.Trim(spec.Path.Value, "\"")
				for _, forbidden := range rule.forbidden {
					if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
						t.Fatalf("%s imports forbidden package %s", filename, importPath)
					}
				}
			}
		}
	}
}
