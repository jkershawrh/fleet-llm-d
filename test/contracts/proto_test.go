//go:build contracts

package contracts

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// protoFiles returns absolute paths of all .proto files under api/proto/.
func protoFiles(t *testing.T) []string {
	t.Helper()
	protoDir := filepath.Join(testRootDir, "api", "proto")
	var files []string
	err := filepath.Walk(protoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".proto") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk %s: %v", protoDir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .proto files found under %s", protoDir)
	}
	return files
}

// TestProtoFilesExist verifies that a .proto file exists for each expected
// fleet-llm-d domain (fleet, placement, routing, etc.).
func TestProtoFilesExist(t *testing.T) {
	files := protoFiles(t)

	expectedDomains := []string{
		"fleet",
		"placement",
		"routing",
		"kvcache",
		"lifecycle",
		"observability",
		"tenant",
	}

	for _, domain := range expectedDomains {
		found := false
		for _, f := range files {
			if strings.Contains(filepath.ToSlash(f), "/"+domain+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no .proto file found for domain %q", domain)
		}
	}

	t.Logf("found %d .proto files across %d expected domains", len(files), len(expectedDomains))
}

// TestProtoServiceDeclarations verifies that every .proto file contains at
// least one gRPC service declaration.
func TestProtoServiceDeclarations(t *testing.T) {
	for _, f := range protoFiles(t) {
		rel, _ := filepath.Rel(testRootDir, f)
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !strings.Contains(string(data), "service ") {
				t.Errorf("%s does not contain a service declaration", rel)
			}
		})
	}
}

// TestProtoGoPackageOptions verifies that every .proto file contains both
// an option go_package directive and a proper package declaration.
func TestProtoGoPackageOptions(t *testing.T) {
	for _, f := range protoFiles(t) {
		rel, _ := filepath.Rel(testRootDir, f)
		t.Run(rel, func(t *testing.T) {
			fh, err := os.Open(f)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer fh.Close()

			var hasGoPackage, hasPackageDecl bool
			scanner := bufio.NewScanner(fh)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "option go_package") {
					hasGoPackage = true
				}
				// Match "package foo.bar.baz;" but not "package;" alone.
				if strings.HasPrefix(line, "package ") {
					hasPackageDecl = true
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan: %v", err)
			}

			if !hasGoPackage {
				t.Errorf("%s is missing option go_package", rel)
			}
			if !hasPackageDecl {
				t.Errorf("%s is missing package declaration", rel)
			}
		})
	}
}
