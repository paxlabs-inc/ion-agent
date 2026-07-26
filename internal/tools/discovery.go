package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// RegistrationSite identifies a source-level registry.Register declaration.
type RegistrationSite struct {
	Path string
	Line int
}

// Discover scans Go source for registry.Register calls. It does not execute
// source or rely on init side effects, preserving deterministic startup.
func Discover(
	ctx context.Context,
	filesystem fs.FS,
	root string,
) ([]RegistrationSite, error) {
	if filesystem == nil {
		return nil, fmt.Errorf("tools: discovery filesystem is required")
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	var sites []RegistrationSite
	err := fs.WalkDir(filesystem, root, func(filePath string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.IsDir() || path.Ext(filePath) != ".go" ||
			strings.HasSuffix(filePath, "_test.go") {
			return nil
		}
		source, err := fs.ReadFile(filesystem, filePath)
		if err != nil {
			return fmt.Errorf("tools: read discovery file %s: %w", filePath, err)
		}
		if !strings.Contains(string(source), "registry") ||
			!strings.Contains(string(source), "Register") {
			return nil
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, filePath, source, 0)
		if err != nil {
			return fmt.Errorf("tools: parse discovery file %s: %w", filePath, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Register" {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok || identifier.Name != "registry" {
				return true
			}
			sites = append(sites, RegistrationSite{
				Path: filePath,
				Line: fileSet.Position(call.Pos()).Line,
			})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sites, func(left, right int) bool {
		if sites[left].Path == sites[right].Path {
			return sites[left].Line < sites[right].Line
		}
		return sites[left].Path < sites[right].Path
	})
	return sites, nil
}
