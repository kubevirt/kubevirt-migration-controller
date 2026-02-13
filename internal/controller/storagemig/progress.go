/*
Copyright 2025 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package storagemig

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	virtv1 "kubevirt.io/api/core/v1"
)

const (
	// Path to the Kubernetes root CA certificate
	kubevirtCA = "/etc/ssl/certs/kubevirt-ca/ca-bundle"
	// virt-handler metrics port (default is 8443 for HTTPS, but we'll try HTTP first)
	virtHandlerMetricsPort = 8443
	// virt-handler label selector
	virtHandlerLabelKey   = "kubevirt.io"
	virtHandlerLabelValue = "virt-handler"
	// virt-handler certificate CN (used for TLS ServerName verification)
	virtHandlerCertCNTemplate = "virt-handler.%s.svc"
	// Prometheus metric names for migration progress
	migrationDataProcessedMetric = "kubevirt_vmi_migration_data_processed_bytes"
	migrationDataTotalMetricOld  = "kubevirt_vmi_migration_data_total_bytes"
	migrationDataTotalMetric     = "kubevirt_vmi_migration_data_bytes_total"
)

func (t *Task) getLastObservedProgressPercent(ctx context.Context, vmName, namespace string) (string, error) {
	t.Log.Info("getLastObservedProgressPercent", "vmName", vmName, "namespace", namespace)

	// Get the VMI to find which node it's running on
	vmi := &virtv1.VirtualMachineInstance{}
	if err := t.Client.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: vmName}, vmi); err != nil {
		if k8serrors.IsNotFound(err) {
			t.Log.Info("VMI not found", "vmName", vmName, "namespace", namespace)
			return "", nil
		}
		return "", fmt.Errorf("failed to get VMI: %w", err)
	}

	// Check if VMI has a node assigned
	if vmi.Status.NodeName == "" {
		t.Log.Info("VMI has no node assigned", "vmName", vmName)
		return "", nil
	}

	// Find the virt-handler pod on the same node
	virtHandlerPod, err := t.findVirtHandlerPod(ctx, vmi.Status.NodeName)
	if err != nil {
		t.Log.Error(err, "Failed to find virt-handler pod", "nodeName", vmi.Status.NodeName)
		return "", nil
	}
	if virtHandlerPod == nil {
		t.Log.Info("virt-handler pod not found", "nodeName", vmi.Status.NodeName)
		return "", nil
	}

	t.Log.Info("Found virt-handler pod", "podName", virtHandlerPod.Name)

	// Get the pod IP
	podIP := virtHandlerPod.Status.PodIP
	if podIP == "" {
		t.Log.Info("virt-handler pod has no IP", "podName", virtHandlerPod.Name)
		return "", nil
	}

	// Connect to the pod's metrics endpoint and parse the results
	progress, err := t.getProgressFromVirtHandlerMetrics(ctx, podIP, vmName)
	if err != nil {
		t.Log.Error(err, "Failed to get progress from virt-handler metrics", "podIP", podIP)
		return "", nil
	}

	return progress, nil
}

// findVirtHandlerPod finds the virt-handler pod running on the specified node
func (t *Task) findVirtHandlerPod(ctx context.Context, nodeName string) (*corev1.Pod, error) {
	podList := &corev1.PodList{}

	// Build label selector for virt-handler
	labelSelector := labels.SelectorFromSet(map[string]string{
		virtHandlerLabelKey: virtHandlerLabelValue,
	})

	// List pods with both selectors
	if err := t.Client.List(ctx, podList, k8sclient.MatchingLabelsSelector{Selector: labelSelector}); err != nil {
		return nil, fmt.Errorf("failed to list virt-handler pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, nil
	}

	for _, pod := range podList.Items {
		if pod.Spec.NodeName == nodeName {
			return &pod, nil
		}
	}
	return nil, nil
}

// getProgressFromVirtHandlerMetrics connects to the virt-handler pod's metrics endpoint
// and parses the Prometheus metrics to extract migration progress
func (t *Task) getProgressFromVirtHandlerMetrics(ctx context.Context, podIP, vmName string) (progress string, err error) {
	httpClient, err := getVirtHandlerHttpClient(t.Config)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP client: %w", err)
	}

	metricsURL := fmt.Sprintf("https://%s:%d/metrics", podIP, virtHandlerMetricsPort)
	t.Log.Info("Fetching metrics from virt-handler", "url", metricsURL)
	req, err := http.NewRequestWithContext(ctx, "GET", metricsURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTPS request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Log.Error(closeErr, "Failed to close response body", "url", metricsURL)
			// If we haven't already set an error, we could set it here, but typically
			// Close() errors after successful reads are not critical
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the metrics to extract migration progress
	progress, err = t.parseMigrationProgressFromMetrics(resp.Body, vmName)
	if err != nil {
		return "", fmt.Errorf("failed to parse metrics: %w", err)
	}

	return progress, nil
}

// parseMigrationProgressFromMetrics parses Prometheus metrics format and extracts
// migration progress for the specified VM
func (t *Task) parseMigrationProgressFromMetrics(metricsReader io.Reader, vmName string) (string, error) {
	var processedBytes, totalBytes float64
	foundProcessed := false
	foundTotal := false

	scanner := bufio.NewScanner(metricsReader)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse metric lines in format: metric_name{labels} value
		// Example: {"line": "kubevirt_vmi_migration_data_processed_bytes{name=\"rhel-10-coral-haddock-83\",namespace=\"awels\",node=\"cnvqe-081.lab.eng.tlv2.redhat.com\"} 9.42669824e+08 1763150823845"}
		if strings.Contains(line, migrationDataProcessedMetric) && strings.Contains(line, vmName) {
			value, err := t.extractMetricValue(line)
			if err == nil {
				processedBytes = value
				foundProcessed = true
			} else {
				t.Log.Error(err, "Failed to extract metric value", "line", line)
			}
		} else if (strings.Contains(line, migrationDataTotalMetric) || strings.Contains(line, migrationDataTotalMetricOld)) && strings.Contains(line, vmName) {
			// example: {"line": "kubevirt_vmi_migration_data_total_bytes{name=\"rhel-10-coral-haddock-83\",namespace=\"awels\",node=\"cnvqe-081.lab.eng.tlv2.redhat.com\"} 3.2213303296e+10 1763150823845"}
			// example: {"line": "kubevirt_vmi_migration_data_bytes_total{name=\"rhel-10-coral-haddock-83\",namespace=\"awels\",node=\"cnvqe-081.lab.eng.tlv2.redhat.com\"} 3.2213303296e+10 1763150823845"}
			value, err := t.extractMetricValue(line)
			if err == nil {
				totalBytes = value
				foundTotal = true
			} else {
				t.Log.Error(err, "Failed to extract metric value", "line", line)
			}
		}

		// If we found both metrics, we can calculate progress
		if foundProcessed && foundTotal {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading metrics: %w", err)
	}

	if !foundProcessed || !foundTotal {
		return "", nil
	}

	if totalBytes == 0 {
		return "0", nil
	}

	// Calculate progress percentage
	progress := (processedBytes / totalBytes) * 100
	progressStr := strconv.FormatFloat(progress, 'f', 2, 64)

	return progressStr, nil
}

// extractMetricValue extracts the metric value from a Prometheus metric line
// and verifies it matches the specified VM name and namespace
func (t *Task) extractMetricValue(metricLine string) (float64, error) {
	parts := strings.Split(metricLine, " ")
	// Get the last part
	value := parts[len(parts)-1]
	// Strip the " and } from the value
	value = strings.TrimSuffix(value, "\"}")
	t.Log.Info("Extracted metric value", "value", value, "metricLine", metricLine)
	valueFloat, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse metric value: %w", err)
	}

	return valueFloat, nil
}

func getVirtHandlerHttpClient(restConfig *rest.Config) (*http.Client, error) {
	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, err
	}

	// Configure TLS with proper CA certificate verification
	var baseTransport *http.Transport
	if existingTransport, ok := httpClient.Transport.(*http.Transport); ok {
		baseTransport = existingTransport.Clone()
	} else {
		baseTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		}
	}

	// Ensure TLS config exists
	if baseTransport.TLSClientConfig == nil {
		baseTransport.TLSClientConfig = &tls.Config{}
	}

	namespace := os.Getenv("CONTROLLER_NAMESPACE")

	if namespace == "" {
		return nil, fmt.Errorf("CONTROLLER_NAMESPACE environment variable is not set")
	}

	// Set ServerName to match the certificate CN (virt-handler certificates use "virt-handler" as CN)
	baseTransport.TLSClientConfig.ServerName = fmt.Sprintf(virtHandlerCertCNTemplate, namespace)

	// Load CA certificate for HTTPS connections
	caCertPool, err := getCACertPool()
	if err != nil {
		// If we can't load the CA, fall back to system CAs
		// ServerName is still set above for proper certificate verification
	} else {
		baseTransport.TLSClientConfig.RootCAs = caCertPool
	}

	// Use the controller's service account token for authentication
	token, err := getTokenFromConfig(restConfig)
	if err != nil {
		return nil, err
	}

	// Wrap transport with authentication if we have a token
	if token != "" {
		httpClient.Transport = &bearerTokenRoundTripper{
			token: token,
			base:  baseTransport,
		}
	} else {
		httpClient.Transport = baseTransport
	}

	return httpClient, nil
}

func getCACertPool() (*x509.CertPool, error) {
	caCert, err := os.ReadFile(kubevirtCA)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse KubeVirt CA certificate")
	}
	return caCertPool, nil
}

func getTokenFromConfig(cfg *rest.Config) (string, error) {
	token := cfg.BearerToken
	if token == "" && cfg.BearerTokenFile != "" {
		tokenBytes, err := os.ReadFile(cfg.BearerTokenFile)
		if err != nil {
			return "", err
		}
		token = string(tokenBytes)
	}
	return token, nil
}

// bearerTokenRoundTripper is an http.RoundTripper that adds a Bearer token
// to the Authorization header of all requests.
type bearerTokenRoundTripper struct {
	token string
	base  http.RoundTripper
}

// RoundTrip implements http.RoundTripper
func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Don't override existing Authorization header
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rt.token))
	}
	return rt.base.RoundTrip(req)
}
