/*
Copyright The KubeVirt Authors.

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
	"crypto/tls"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-operator/api/v1alpha1"
)

var _ = Describe("ManagedTLSWatcher", func() {
	var (
		watcher   *ManagedTLSWatcher
		namespace *corev1.Namespace
		testNs    string
	)

	BeforeEach(func() {
		testNs = "tls-watcher-test-" + rand.String(5)
		watcher = NewManagedTLSWatcher(testNs)

		// Create test namespace
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNs,
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
		})
	})

	Describe("GetTLSConfig", func() {
		DescribeTable("should return default config",
			func(setupWatcher func() *ManagedTLSWatcher) {
				w := setupWatcher()
				w.SetCache(mgr.GetCache())
				w.ready = true
				config := w.GetTLSConfig(context.Background())
				Expect(config).NotTo(BeNil())
				Expect(config.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
			},
			Entry("when namespace is empty", func() *ManagedTLSWatcher {
				return NewManagedTLSWatcher("")
			}),
			Entry("when namespace is set but no MigController exists", func() *ManagedTLSWatcher {
				return watcher
			}),
			Entry("when MigController exists in a different namespace", func() *ManagedTLSWatcher {
				// Create another namespace
				otherNs := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "other-namespace-" + rand.String(5),
					},
				}
				Expect(k8sClient.Create(ctx, otherNs)).To(Succeed())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, otherNs)).To(Succeed())
				})

				// Create a MigController in a different namespace
				migController := &migrationsv1alpha1.MigController{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-controller",
						Namespace: otherNs.Name,
					},
					Spec: migrationsv1alpha1.MigControllerSpec{
						TLSSecurityProfile: &migrationsv1alpha1.TLSSecurityProfile{
							Type: migrationsv1alpha1.TLSProfileOldType,
							Old:  &migrationsv1alpha1.OldTLSProfile{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, migController)).To(Succeed())

				// watcher already set to testNs in BeforeEach
				return watcher
			}),
		)

		Context("when MigController exists in the namespace", func() {
			It("should use TLS config from MigController", func() {
				// Create a MigController with custom TLS profile
				migController := &migrationsv1alpha1.MigController{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-controller",
						Namespace: testNs,
					},
					Spec: migrationsv1alpha1.MigControllerSpec{
						TLSSecurityProfile: &migrationsv1alpha1.TLSSecurityProfile{
							Type: migrationsv1alpha1.TLSProfileOldType,
							Old:  &migrationsv1alpha1.OldTLSProfile{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, migController)).To(Succeed())

				watcher.SetCache(mgr.GetCache())
				watcher.ready = true

				// Wait for cache to sync the new MigController
				Eventually(func() uint16 {
					config := watcher.GetTLSConfig(context.Background())
					return config.MinVersion
				}).Should(Equal(uint16(tls.VersionTLS10)), "Old profile should allow TLS 1.0")
			})
		})

	})

	Describe("SetCache", func() {
		It("should set the cache", func() {
			watcher.SetCache(mgr.GetCache())
			Expect(watcher.cache).NotTo(BeNil())
		})
	})
})
