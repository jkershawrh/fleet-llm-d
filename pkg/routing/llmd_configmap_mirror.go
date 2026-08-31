package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KubernetesConfigMapMirrorOptions configures the per-EPP watched-file
// mirror. It avoids kubelet projected-volume refresh latency by polling the
// authoritative ConfigMap and atomically replacing files in a shared
// emptyDir mounted by the EPP.
type KubernetesConfigMapMirrorOptions struct {
	APIURL      string
	Namespace   string
	Name        string
	Directory   string
	TokenFile   string
	Interval    time.Duration
	HTTPClient  *http.Client
	OnPublished func(resourceVersion string)
	OnError     func(error)
}

type mirroredConfigMap struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Data map[string]string `json:"data"`
}

// RunKubernetesConfigMapMirror performs an initial synchronous publication,
// then polls until ctx is cancelled. A failed read never replaces the last
// valid local files.
func RunKubernetesConfigMapMirror(ctx context.Context, opts KubernetesConfigMapMirrorOptions) error {
	if _, err := url.ParseRequestURI(opts.APIURL); err != nil || (!strings.HasPrefix(opts.APIURL, "https://") && !strings.HasPrefix(opts.APIURL, "http://")) {
		return fmt.Errorf("invalid Kubernetes API URL")
	}
	if opts.Namespace == "" || opts.Name == "" || opts.Directory == "" || opts.TokenFile == "" {
		return fmt.Errorf("ConfigMap namespace, name, output directory, and token file are required")
	}
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	lastVersion := ""
	lastFiles := make(map[string]struct{})
	syncOnce := func() error {
		token, err := os.ReadFile(opts.TokenFile)
		if err != nil {
			return fmt.Errorf("read Kubernetes bearer token: %w", err)
		}
		if strings.TrimSpace(string(token)) == "" {
			return fmt.Errorf("read Kubernetes bearer token: token file is empty")
		}
		endpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps/%s", strings.TrimRight(opts.APIURL, "/"), url.PathEscape(opts.Namespace), url.PathEscape(opts.Name))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		resp, err := opts.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("read Router ConfigMap: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("read Router ConfigMap: Kubernetes API returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
		}
		var configMap mirroredConfigMap
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&configMap); err != nil {
			return fmt.Errorf("decode Router ConfigMap: %w", err)
		}
		if configMap.Metadata.ResourceVersion == "" || configMap.Data == nil {
			return fmt.Errorf("decode Router ConfigMap: resourceVersion and data are required")
		}
		if configMap.Metadata.ResourceVersion == lastVersion {
			return nil
		}
		files := make(map[string][]byte, len(configMap.Data))
		for name, contents := range configMap.Data {
			if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
				return fmt.Errorf("decode Router ConfigMap: unsafe data key %q", name)
			}
			files[name] = []byte(contents)
		}
		if err := (directoryPublisher{directory: opts.Directory}).Publish(ctx, files); err != nil {
			return err
		}
		for name := range lastFiles {
			if _, ok := files[name]; !ok {
				if err := os.Remove(filepath.Join(opts.Directory, name)); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove stale Router endpoint file: %w", err)
				}
			}
		}
		lastFiles = make(map[string]struct{}, len(files))
		for name := range files {
			lastFiles[name] = struct{}{}
		}
		lastVersion = configMap.Metadata.ResourceVersion
		if opts.OnPublished != nil {
			opts.OnPublished(lastVersion)
		}
		return nil
	}

	if err := syncOnce(); err != nil {
		return err
	}
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := syncOnce(); err != nil {
				if opts.OnError != nil {
					opts.OnError(err)
				}
			}
		}
	}
}
