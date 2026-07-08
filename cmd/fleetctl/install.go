package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"
)

func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	name := fs.String("name", "fleet-llm-d", "Release name")
	namespace := fs.String("namespace", "fleet-llm-d", "Target namespace")
	mode := fs.String("mode", "all", "Server mode: all, control, inference")
	image := fs.String("image", "quay.io/fleet-llm-d/fleet-controller:latest", "Controller image")
	secret := fs.String("secret", "", "Auth secret (will create K8s Secret)")
	_ = fs.Parse(args)

	fmt.Printf("Installing fleet-llm-d...\n")
	fmt.Printf("  Name:      %s\n", *name)
	fmt.Printf("  Namespace: %s\n", *namespace)
	fmt.Printf("  Mode:      %s\n", *mode)
	fmt.Printf("  Image:     %s\n", *image)

	// Check if helm is available
	helmPath, err := exec.LookPath("helm")
	if err == nil {
		fmt.Printf("\nHelm detected at %s. Run:\n\n", helmPath)
		fmt.Printf("  helm install %s ./charts/fleet-llm-d \\\n", *name)
		fmt.Printf("    --namespace %s --create-namespace \\\n", *namespace)
		fmt.Printf("    --set mode=%s \\\n", *mode)
		fmt.Printf("    --set image.repository=%s \\\n", strings.Split(*image, ":")[0])
		if parts := strings.SplitN(*image, ":", 2); len(parts) == 2 {
			fmt.Printf("    --set image.tag=%s\n", parts[1])
		}
	} else {
		fmt.Printf("\nHelm not found. Use kubectl/oc:\n\n")
		fmt.Printf("  oc new-project %s\n", *namespace)
		fmt.Printf("  oc apply -f deploy/kustomize/overlays/standalone/\n")
	}

	if *secret != "" {
		fmt.Printf("\nCreate auth secret:\n")
		fmt.Printf("  kubectl create secret generic fleet-auth \\\n")
		fmt.Printf("    --namespace %s \\\n", *namespace)
		fmt.Printf("    --from-literal=FLEET_AUTH_SECRET=%s\n", *secret)
	}
}
