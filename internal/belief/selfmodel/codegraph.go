package selfmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"lukechampine.com/blake3"
)

// CodeGraph is a deterministic structural snapshot derived directly from Go
// source. It deliberately contains no hand-written capability claims.
type CodeGraph struct {
	Root     string        `json:"root"`
	Digest   string        `json:"digest"`
	Packages []CodePackage `json:"packages"`
	Symbols  []CodeSymbol  `json:"symbols"`
}

// NewFromBuildInfo constructs a code-derived structural model from the running
// Go binary when source files are not installed beside the executable. Source
// parsing remains preferred because it includes symbols; build metadata is the
// production-safe fallback and still prevents hand-written capability claims.
func NewFromBuildInfo(
	clock Clock,
	core *ImmutableCore,
) (*Model, error) {
	info, ok := debug.ReadBuildInfo()
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("selfmodel: resolve executable: %w", err)
	}
	graph := CodeGraph{Root: "binary:" + executable}
	binaryDigest := ""
	if ok && strings.TrimSpace(info.Main.Path) != "" {
		graph.Packages = append(graph.Packages, CodePackage{
			Path: info.Main.Path, Name: filepath.Base(info.Main.Path),
		})
		for _, dependency := range info.Deps {
			if dependency == nil || strings.TrimSpace(dependency.Path) == "" {
				continue
			}
			graph.Packages = append(graph.Packages, CodePackage{
				Path: dependency.Path, Name: filepath.Base(dependency.Path),
			})
		}
	} else {
		binaryDigest, err = executableDigest(executable)
		if err != nil {
			return nil, err
		}
		graph.Packages = append(graph.Packages, CodePackage{
			Path: graph.Root, Name: filepath.Base(executable),
		})
	}
	sort.Slice(graph.Packages, func(left, right int) bool {
		return graph.Packages[left].Path < graph.Packages[right].Path
	})
	encoded, err := json.Marshal(struct {
		GoVersion string               `json:"go_version"`
		Main      moduleIdentity       `json:"main"`
		Packages  []CodePackage        `json:"packages"`
		Settings  []debug.BuildSetting `json:"settings"`
		Binary    string               `json:"binary_digest,omitempty"`
	}{
		GoVersion: buildInfoGoVersion(info, ok),
		Main: moduleIdentity{
			Path:    buildInfoMainPath(info, ok, graph.Root),
			Version: buildInfoMainVersion(info, ok),
		},
		Packages: graph.Packages,
		Settings: buildInfoSettings(info, ok),
		Binary:   binaryDigest,
	})
	if err != nil {
		return nil, fmt.Errorf("selfmodel: encode build graph: %w", err)
	}
	digest := blake3.Sum256(encoded)
	graph.Digest = fmt.Sprintf("%x", digest[:])
	model, err := New(clock, core)
	if err != nil {
		return nil, err
	}
	model.self.Structure = &graph
	return model, nil
}

func buildInfoGoVersion(info *debug.BuildInfo, ok bool) string {
	if ok && info != nil {
		return info.GoVersion
	}
	return ""
}

func buildInfoMainPath(info *debug.BuildInfo, ok bool, fallback string) string {
	if ok && info != nil && strings.TrimSpace(info.Main.Path) != "" {
		return info.Main.Path
	}
	return fallback
}

func buildInfoMainVersion(info *debug.BuildInfo, ok bool) string {
	if ok && info != nil {
		return info.Main.Version
	}
	return ""
}

func buildInfoSettings(info *debug.BuildInfo, ok bool) []debug.BuildSetting {
	if ok && info != nil {
		return append([]debug.BuildSetting(nil), info.Settings...)
	}
	return nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("selfmodel: open executable: %w", err)
	}
	defer file.Close()
	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("selfmodel: hash executable: %w", err)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

type moduleIdentity struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// CodePackage is one parsed Go package and its source-level import edges.
type CodePackage struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Imports []string `json:"imports"`
}

// CodeSymbol is one declared function, method, interface, struct, or named
// type in the codegraph.
type CodeSymbol struct {
	Package  string `json:"package"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Receiver string `json:"receiver,omitempty"`
}

// DeriveCodeGraph parses the actual Go source rooted at root and produces a
// canonical BLAKE3-identified snapshot.
func DeriveCodeGraph(ctx context.Context, root string) (CodeGraph, error) {
	if err := ctx.Err(); err != nil {
		return CodeGraph{}, err
	}
	if strings.TrimSpace(root) == "" {
		return CodeGraph{}, fmt.Errorf("selfmodel: codegraph root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return CodeGraph{}, fmt.Errorf("selfmodel: resolve codegraph root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return CodeGraph{}, fmt.Errorf("selfmodel: stat codegraph root: %w", err)
	}
	if !info.IsDir() {
		return CodeGraph{}, fmt.Errorf("selfmodel: codegraph root must be a directory")
	}

	type packageAccumulator struct {
		name    string
		imports map[string]struct{}
	}
	packages := make(map[string]*packageAccumulator)
	var symbols []CodeSymbol
	filesystem := os.DirFS(absolute)
	err = fs.WalkDir(filesystem, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." && shouldSkipCodegraphDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := fs.ReadFile(filesystem, path)
		if err != nil {
			return fmt.Errorf("selfmodel: read %s: %w", path, err)
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, source, 0)
		if err != nil {
			return fmt.Errorf("selfmodel: parse %s: %w", path, err)
		}
		packagePath := filepath.ToSlash(filepath.Dir(path))
		if packagePath == "." {
			packagePath = ""
		}
		accumulator := packages[packagePath]
		if accumulator == nil {
			accumulator = &packageAccumulator{
				name:    parsed.Name.Name,
				imports: make(map[string]struct{}),
			}
			packages[packagePath] = accumulator
		}
		for _, spec := range parsed.Imports {
			accumulator.imports[strings.Trim(spec.Path.Value, `"`)] = struct{}{}
		}
		for _, declaration := range parsed.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				receiver := ""
				kind := "function"
				if typed.Recv != nil && len(typed.Recv.List) > 0 {
					receiver = expressionName(typed.Recv.List[0].Type)
					kind = "method"
				}
				symbols = append(symbols, CodeSymbol{
					Package: packagePath, Name: typed.Name.Name,
					Kind: kind, Receiver: receiver,
				})
			case *ast.GenDecl:
				if typed.Tok != token.TYPE {
					continue
				}
				for _, declarationSpec := range typed.Specs {
					specification, ok := declarationSpec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					kind := "type"
					switch specification.Type.(type) {
					case *ast.StructType:
						kind = "struct"
					case *ast.InterfaceType:
						kind = "interface"
					}
					symbols = append(symbols, CodeSymbol{
						Package: packagePath,
						Name:    specification.Name.Name,
						Kind:    kind,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return CodeGraph{}, err
	}

	graph := CodeGraph{Root: absolute}
	for packagePath, accumulator := range packages {
		imports := make([]string, 0, len(accumulator.imports))
		for imported := range accumulator.imports {
			imports = append(imports, imported)
		}
		sort.Strings(imports)
		graph.Packages = append(graph.Packages, CodePackage{
			Path: packagePath, Name: accumulator.name, Imports: imports,
		})
	}
	sort.Slice(graph.Packages, func(i, j int) bool {
		return graph.Packages[i].Path < graph.Packages[j].Path
	})
	sort.Slice(symbols, func(i, j int) bool {
		left, right := symbols[i], symbols[j]
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Receiver < right.Receiver
	})
	graph.Symbols = symbols
	if len(graph.Packages) == 0 {
		return CodeGraph{}, fmt.Errorf("selfmodel: codegraph contains no Go packages")
	}
	encoded, err := json.Marshal(struct {
		Packages []CodePackage `json:"packages"`
		Symbols  []CodeSymbol  `json:"symbols"`
	}{
		Packages: graph.Packages,
		Symbols:  graph.Symbols,
	})
	if err != nil {
		return CodeGraph{}, fmt.Errorf("selfmodel: encode codegraph: %w", err)
	}
	digest := blake3.Sum256(encoded)
	graph.Digest = fmt.Sprintf("%x", digest[:])
	return graph, nil
}

func shouldSkipCodegraphDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "research":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func expressionName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + expressionName(typed.X)
	case *ast.IndexExpr:
		return expressionName(typed.X)
	case *ast.IndexListExpr:
		return expressionName(typed.X)
	case *ast.SelectorExpr:
		return expressionName(typed.X) + "." + typed.Sel.Name
	default:
		return ""
	}
}
