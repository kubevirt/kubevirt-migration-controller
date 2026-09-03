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

package componenthelpers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-operator/api/v1alpha1"
)

var (
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	k8sClient  client.Client
	cfg        *rest.Config
	mgr        ctrl.Manager
	tempCRDDir string
)

func TestComponentHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Component Helpers Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	Expect(migrationsv1alpha1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())

	By("downloading MigController CRD from operator repository")
	tempCRDDir, err := os.MkdirTemp("", "operator-crds-")
	Expect(err).NotTo(HaveOccurred())

	operatorBranch := getOperatorBranch()
	crdURL := fmt.Sprintf(
		"https://raw.githubusercontent.com/kubevirt/kubevirt-migration-operator/%s/"+
			"config/crd/bases/migrations.kubevirt.io_migcontrollers.yaml",
		operatorBranch,
	)

	err = downloadFile(filepath.Join(tempCRDDir, "migrations.kubevirt.io_migcontrollers.yaml"), crdURL)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			tempCRDDir,
		},
		ErrorIfCRDPathMissing: false,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if dir := getFirstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(mgr).NotTo(BeNil())

	By("starting manager")
	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())

	By("cleaning up temporary CRD directory")
	if tempCRDDir != "" {
		Expect(os.RemoveAll(tempCRDDir)).To(Succeed())
	}
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
func getFirstFoundEnvTestBinaryDir() string {
	wd, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	basePath := filepath.Join(wd, "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// getOperatorBranch determines the corresponding operator branch based on the current git branch.
func getOperatorBranch() string {
	cmd := exec.Command("git", "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		logf.Log.Info("Failed to get current git branch, defaulting to main", "error", err)
		return "main"
	}

	currentBranch := string(output)
	currentBranch = currentBranch[:len(currentBranch)-1] // trim newline

	releasePattern := regexp.MustCompile(`^release-([0-9]+\.[0-9]+)$`)
	if matches := releasePattern.FindStringSubmatch(currentBranch); matches != nil {
		return "release-" + matches[1]
	}

	return "main"
}

// downloadFile downloads a file from url and saves it to filepath.
func downloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: status code %d", url, resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, resp.Body)
	return err
}
