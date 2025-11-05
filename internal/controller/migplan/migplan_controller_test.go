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

package migplan

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	virtv1 "kubevirt.io/api/core/v1"
	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

var _ = PDescribe("MigPlan Controller envtests - with minimal real apiserver", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		migplan := &migrationsv1alpha1.MigPlan{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MigPlan")
			err := k8sClient.Get(ctx, typeNamespacedName, migplan)
			if err != nil && errors.IsNotFound(err) {
				resource := &migrationsv1alpha1.MigPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &migrationsv1alpha1.MigPlan{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance MigPlan")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &MigPlanReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

var _ = Describe("MigPlan Controller tests without apiserver", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		var reconciler *MigPlanReconciler

		BeforeEach(func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects().
				WithStatusSubresource(&migrationsv1alpha1.MigPlan{}).
				Build()

			reconciler = &MigPlanReconciler{
				Client:        fakeClient,
				Scheme:        scheme.Scheme,
				EventRecorder: record.NewFakeRecorder(10),
			}
		})

		AfterEach(func() {
			if reconciler != nil {
				close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
				reconciler = nil
			}
		})

		DescribeTable("validateKubeVirtInstalled sets correct conditions",
			func(kv *virtv1.KubeVirt, expectType string, expectStatus string) {
				if kv != nil {
					Expect(reconciler.Client.Create(ctx, kv)).To(Succeed())
				}

				migPlan := newMigPlan(resourceName)
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrationsv1alpha1.MigPlan{}
				err = reconciler.Client.Get(ctx, typeNamespacedName, updated)
				Expect(err).NotTo(HaveOccurred())

				Expect(updated.Status.Conditions.List).To(ContainElement(
					And(
						HaveField("Type", expectType),
						HaveField("Status", expectStatus),
					),
				), "Expected conditions differ from found")
			},
			Entry("no KubeVirt objects", nil, "KubeVirtNotInstalledSourceCluster", "True"),
			Entry("invalid operator version", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: "default"},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "not-a-version",
				},
			}, "KubeVirtVersionNotSupported", "True"),
			Entry("operator version < 1.3.0", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: "default"},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.2.0",
				},
			}, "KubeVirtVersionNotSupported", "True"),
			Entry("operator version >= 1.3.0 but rollout strategy not set", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: "default"},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.3.0",
				},
			}, "KubeVirtStorageLiveMigrationNotEnabled", "True"),
			Entry("operator version >= 1.3.0, rollout strategy not LiveUpdate", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: "default"},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyStage),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.3.0",
				},
			}, "KubeVirtStorageLiveMigrationNotEnabled", "True"),
		)

		Context("kv installed and appropriate", func() {
			BeforeEach(func() {
				kv := &virtv1.KubeVirt{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kv",
						Namespace: "default",
					},
					Spec: virtv1.KubeVirtSpec{
						Configuration: virtv1.KubeVirtConfiguration{
							VMRolloutStrategy:      ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
							DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
						},
					},
					Status: virtv1.KubeVirtStatus{
						OperatorVersion: "v1.5.0",
					},
				}
				Expect(reconciler.Client.Create(ctx, kv)).To(Succeed())
			})

			It("should have no conditions when src MigCluster does not exist", func() {
				// Create a MigPlan referencing a non-existent src MigCluster
				migPlan := newMigPlan(resourceName)
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrationsv1alpha1.MigPlan{}
				err = reconciler.Client.Get(ctx, typeNamespacedName, updated)
				Expect(err).NotTo(HaveOccurred())

				Expect(updated.Status.Conditions.List).To(BeEmpty())
			})
		})
	})
})

func newMigPlan(name string) *migrationsv1alpha1.MigPlan {
	return &migrationsv1alpha1.MigPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: migrationsv1alpha1.MigPlanSpec{},
	}
}
