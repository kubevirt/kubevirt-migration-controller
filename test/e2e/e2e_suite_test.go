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

package e2e

import (
	"flag"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/test/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	kubectlPath                  = flag.String("kubectl-path", "kubectl", "Path to the kubectl binary")
	kubeConfig                   = flag.String("test-kubeconfig", "", "Path to the kubeconfig file")
	migrationControllerNamespace = flag.String("migration-controller-namespace", "kubevirt-migration-system",
		"Namespace of the migration controller")
	kubeURL = flag.String("kubeurl", "", "URL of the kube API server")
	c       client.Client
)

const (
	registryProxyCACertName = "registry-proxy-ca"
	registryProxyNamespace  = "nginx-proxy"
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purposed to be used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kubevirt-migration-controller integration test suite\n")
	BuildTestSuite()
	RunSpecs(t, "e2e suite")
}

func BuildTestSuite() {
	SynchronizedBeforeSuite(func() {}, func() {
		scheme := runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(virtv1.AddToScheme(scheme))
		utilruntime.Must(cdiv1.AddToScheme(scheme))
		utilruntime.Must(routev1.AddToScheme(scheme))
		utilruntime.Must(ocpconfigv1.AddToScheme(scheme))
		utilruntime.Must(migrations.AddToScheme(scheme))
		By("checking if cert manager is installed")
		isCertManagerAlreadyInstalled := utils.IsCertManagerCRDsInstalled(kubectlPath)
		Expect(isCertManagerAlreadyInstalled).To(BeTrue(), "CertManager is not installed")

		_, err := fmt.Fprintf(GinkgoWriter, "Reading parameters\n")
		Expect(err).ToNot(HaveOccurred(), "Unable to write to GinkgoWriter")

		_, err = fmt.Fprintf(GinkgoWriter, "Kubectl path: %s\n", *kubectlPath)
		Expect(err).ToNot(HaveOccurred(), "Unable to write to GinkgoWriter")

		_, err = fmt.Fprintf(GinkgoWriter, "Kubeconfig: %s\n", *kubeConfig)
		Expect(err).ToNot(HaveOccurred(), "Unable to write to GinkgoWriter")

		_, err = fmt.Fprintf(GinkgoWriter, "KubeURL: %s\n", *kubeURL)
		Expect(err).ToNot(HaveOccurred(), "Unable to write to GinkgoWriter")

		_, err = fmt.Fprintf(GinkgoWriter, "Migration controller namespace: %s\n", *migrationControllerNamespace)
		Expect(err).ToNot(HaveOccurred(), "Unable to write to GinkgoWriter")

		restConfig, err := LoadConfig()
		Expect(err).ToNot(HaveOccurred(), "Unable to load RestConfig")
		// clients
		c, err = client.New(restConfig, client.Options{Scheme: scheme})
		Expect(err).ToNot(HaveOccurred(), "Unable to create Client")
		Expect(c).ToNot(BeNil(), "Client is nil")
	})
}

// LoadConfig loads our specified kubeconfig
func LoadConfig() (*rest.Config, error) {
	return clientcmd.BuildConfigFromFlags(*kubeURL, *kubeConfig)
}

func GetClient() client.Client {
	return c
}
