package processlaunch

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPrepareRequiresExplicitEnvironmentPolicy(t *testing.T) {
	if _, err := Prepare(Spec{Context: context.Background(), Executable: "tool"}); err == nil {
		t.Fatal("process launch accepted an unspecified environment policy")
	}
	if _, err := Prepare(Spec{Context: context.Background(), Executable: "tool", Environment: EnvironmentInherit, Env: []string{"A=B"}}); err == nil {
		t.Fatal("inherited environment accepted explicit entries")
	}
	if _, err := Prepare(Spec{Context: context.Background(), Executable: "tool", Environment: EnvironmentExact, Arguments: []string{"bad\x00argument"}}); err == nil {
		t.Fatal("process launch accepted a NUL argument")
	}
}

func TestPrepareCopiesArgumentsAndExactEnvironment(t *testing.T) {
	arguments := []string{"first"}
	environment := []string{"LANG=C"}
	command, err := Prepare(Spec{
		Context: context.Background(), Executable: "tool", Arguments: arguments,
		Environment: EnvironmentExact, Env: environment, Directory: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments[0], environment[0] = "changed", "LANG=changed"
	if command.Args[1] != "first" || command.Env[0] != "LANG=C" || command.Dir != "work" {
		t.Fatalf("prepared command retained mutable caller slices: args=%v env=%v dir=%q", command.Args, command.Env, command.Dir)
	}
}

func TestProductionCodeUsesSharedProcessLauncher(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	var violations []string
	err := filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".scratch" || entry.Name() == "node_modules" || entry.Name() == "test-results" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(filepath.ToSlash(path), "/internal/processlaunch/processlaunch.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		execAliases := make(map[string]struct{})
		for _, imported := range parsed.Imports {
			if imported.Path.Value != `"os/exec"` {
				continue
			}
			name := "exec"
			if imported.Name != nil {
				name = imported.Name.Name
			}
			if name == "." {
				position := fileSet.Position(imported.Pos())
				violations = append(violations, filepath.ToSlash(position.Filename)+":"+strconv.Itoa(position.Line))
				continue
			}
			execAliases[name] = struct{}{}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext") {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, imported := execAliases[identifier.Name]; !imported {
				return true
			}
			position := fileSet.Position(selector.Pos())
			violations = append(violations, filepath.ToSlash(position.Filename)+":"+strconv.Itoa(position.Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("production code bypasses internal/processlaunch: %v", violations)
	}
}
