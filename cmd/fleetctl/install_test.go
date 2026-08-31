package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestRunInstall_DefaultFlags(t *testing.T) {
	out := captureStdout(func() {
		runInstall([]string{})
	})

	// Should print the release name, namespace, mode, and image defaults.
	for _, want := range []string{
		"fleet-llm-d",
		"Namespace:",
		"Mode:",
		"all",
		"quay.io/fleet-llm-d/fleet-controller",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRunInstall_CustomFlags(t *testing.T) {
	out := captureStdout(func() {
		runInstall([]string{
			"--name", "my-release",
			"--namespace", "prod-ns",
			"--mode", "inference",
			"--image", "registry.example.com/ctrl:v1.2.3",
		})
	})

	for _, want := range []string{
		"my-release",
		"prod-ns",
		"inference",
		"registry.example.com/ctrl",
		"v1.2.3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRunInstall_WithSecret(t *testing.T) {
	out := captureStdout(func() {
		runInstall([]string{
			"--secret", "s3cret-value",
		})
	})

	if !strings.Contains(out, "fleet-auth") {
		t.Error("expected auth secret creation instructions")
	}
	if !strings.Contains(out, "s3cret-value") {
		t.Error("expected secret value in kubectl command")
	}
}

func TestRunInstall_HelmDetection(t *testing.T) {
	// We cannot guarantee helm is or isn't installed, but we can verify
	// the output contains either the helm path or the kubectl fallback.
	out := captureStdout(func() {
		runInstall([]string{})
	})

	hasHelm := strings.Contains(out, "helm install")
	hasKubectl := strings.Contains(out, "oc apply") || strings.Contains(out, "kubectl")

	if !hasHelm && !hasKubectl {
		t.Error("expected either helm install or kubectl/oc fallback instructions")
	}
}
