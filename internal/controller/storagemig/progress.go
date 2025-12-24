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
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	prometheusapi "github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	prometheusRoute  = "prometheus-k8s"
	prometheusURLKey = "PROMETHEUS_URL"
	progressQuery    = "kubevirt_vmi_migration_data_processed_bytes{name=\"%s\", namespace=\"%s\"} / (kubevirt_vmi_migration_data_processed_bytes{name=\"%s\", namespace=\"%s\"} + kubevirt_vmi_migration_data_remaining_bytes{name=\"%s\", namespace=\"%s\"}) * 100"
)

func (t *Task) getLastObservedProgressPercent(ctx context.Context, vmName, namespace string) (string, error) {
	if err := t.buildPrometheusAPI(ctx); err != nil {
		return "", err
	}

	if t.PrometheusAPI == nil {
		return "", nil
	}
	result, warnings, err := t.PromQuery(ctx, fmt.Sprintf(progressQuery, vmName, namespace, vmName, namespace, vmName, namespace), time.Now())
	if err != nil {
		t.Log.Error(err, "Failed to query prometheus, returning no result")
		return "", nil
	}
	if len(warnings) > 0 {
		t.Log.Info("Warnings", "warnings", warnings)
	}
	t.Log.V(5).Info("Prometheus query result", "type", result.Type(), "value", result.String())
	progress := parseProgress(result.String())
	if progress != "" {
		return progress + "%", nil
	}
	return "", nil
}

func (t *Task) buildPrometheusAPI(ctx context.Context) error {
	if t.PrometheusAPI != nil {
		return nil
	}
	url, err := t.buildSourcePrometheusEndPointURL(ctx)
	if err != nil {
		return err
	}

	// Prometheus URL not found, return blank progress
	if url == "" {
		return nil
	}
	httpClient, err := rest.HTTPClientFor(t.Config)
	if err != nil {
		return err
	}
	client, err := prometheusapi.NewClient(prometheusapi.Config{
		Address: url,
		Client:  httpClient,
	})
	if err != nil {
		return err
	}
	t.PrometheusAPI = prometheusv1.NewAPI(client)
	t.PromQuery = t.PrometheusAPI.Query
	return nil
}

func parseProgress(progress string) string {
	regExp := regexp.MustCompile(`\=\> (\d{1,3})\.\d* @`)

	if regExp.MatchString(progress) {
		return regExp.FindStringSubmatch(progress)[1]
	}
	return ""
}

// Find the URL that contains the prometheus metrics on the source cluster.
func (t *Task) buildSourcePrometheusEndPointURL(ctx context.Context) (string, error) {
	urlString, err := t.getPrometheusURLFromConfig(ctx)
	if err != nil {
		return "", err
	}
	if urlString == "" {
		// URL not found in config map, attempt to get the open shift prometheus route.
		route := &routev1.Route{}
		if err := t.Client.Get(ctx, k8sclient.ObjectKey{Namespace: "openshift-monitoring", Name: "prometheus-k8s"}, route); err != nil {
			if k8serrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				return "", nil
			}
			return "", err
		}
		urlString = route.Spec.Host
	}
	if urlString == "" {
		t.Log.Info("Prometheus route URL not found, returning empty string")
		// Don't return error just return empty and skip the progress report
		return "", nil
	}
	parsedUrl, err := url.Parse(urlString)
	if err != nil {
		return "", err
	}
	parsedUrl.Scheme = "https"
	urlString = parsedUrl.String()
	t.Log.V(3).Info("Prometheus route URL", "url", urlString)
	return urlString, nil
}

// The key in the config map should be in format <source cluster name>_PROMETHEUS_URL
// For instance if the cluster name is "cluster1" the key should be "cluster1_PROMETHEUS_URL"
func (t *Task) getPrometheusURLFromConfig(ctx context.Context) (string, error) {
	migControllerConfigMap := &corev1.ConfigMap{}
	controllerNamespace := os.Getenv("CONTROLLER_NAMESPACE")
	if err := t.UncachedClient.Get(ctx, k8sclient.ObjectKey{Namespace: controllerNamespace, Name: "migration-controller"}, migControllerConfigMap); err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if prometheusURL, found := migControllerConfigMap.Data[prometheusURLKey]; found {
		return prometheusURL, nil
	}
	return "", nil
}
