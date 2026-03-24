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
package multinamespacestoragemig

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("MultiNamespaceStorageMigration controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		var reconciler *MultiNamespaceStorageMigrationReconciler

		BeforeEach(func() {
			reconciler = &MultiNamespaceStorageMigrationReconciler{
				Client:        k8sClient,
				Scheme:        scheme.Scheme,
				EventRecorder: record.NewFakeRecorder(10),
				Log:           logf.Log,
			}
		})

		AfterEach(func() {
			if reconciler != nil {
				close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
				testutils.CleanupResources(ctx, reconciler.Client)
				reconciler = nil
			}
		})

		Context("Cascade delete on multi-namespace migration deletion", func() {
			It("should delete child VirtualMachineStorageMigration resources and remove finalizer when multi-namespace migration is deleted", func() {
				planName := "test-cascade-delete-mig-plan"
				migrationName := "test-cascade-delete-migration"
				nn := types.NamespacedName{Name: migrationName, Namespace: testutils.TestNamespace}
				multiPlan := createMultiNamespaceStorageMigrationPlan(planName)
				multiMigration := createMultiNamespaceStorageMigration(migrationName, planName)
				Expect(reconciler.Client.Create(ctx, multiPlan)).To(Succeed())
				Expect(reconciler.Client.Create(ctx, multiMigration)).To(Succeed())

				By("Reconciling to add finalizer")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())

				By("Reconciling to create child migration")
				_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())

				childMigrationName := migrations.GetNamespacedPlanName(planName, testutils.TestNamespace)
				childMigration := &migrations.VirtualMachineStorageMigration{}
				Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: childMigrationName, Namespace: testutils.TestNamespace}, childMigration)).To(Succeed())

				Expect(reconciler.Client.Get(ctx, nn, multiMigration)).To(Succeed())
				Expect(multiMigration.Finalizers).To(ContainElement(multiNamespaceStorageMigrationFinalizer))

				By("Deleting the multi-namespace migration (sets DeletionTimestamp)")
				Expect(reconciler.Client.Delete(ctx, multiMigration)).To(Succeed())

				By("Reconciling to run finalizer: delete children and remove finalizer")
				_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying child migration was deleted")
				childMigration = &migrations.VirtualMachineStorageMigration{}
				err = reconciler.Client.Get(ctx, types.NamespacedName{Name: childMigrationName, Namespace: testutils.TestNamespace}, childMigration)
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			})
		})

		Context("Naming length constraints", func() {
			It("should respect naming length constraints for namespaced plans", func() {
				name := strings.Repeat("a", kvalidation.DNS1123SubdomainMaxLength)
				nn := types.NamespacedName{Name: name, Namespace: testutils.TestNamespace}
				multinsPlan := createMultiNamespaceStorageMigrationPlan(name)
				multinsMigration := createMultiNamespaceStorageMigration(name, multinsPlan.Name)
				Expect(reconciler.Client.Create(ctx, multinsPlan)).To(Succeed())
				Expect(reconciler.Client.Create(ctx, multinsMigration)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nn,
				})
				Expect(err).NotTo(HaveOccurred())
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nn,
				})
				Expect(err).NotTo(HaveOccurred())
				mig := &migrations.VirtualMachineStorageMigration{}
				mignn := types.NamespacedName{Name: migrations.GetNamespacedPlanName(name, testutils.TestNamespace), Namespace: testutils.TestNamespace}
				Expect(reconciler.Client.Get(ctx, mignn, mig)).To(Succeed())
				Expect(mig.Name).To(HaveLen(kvalidation.DNS1123SubdomainMaxLength))
				Expect(mig.Name).To(Equal(fmt.Sprintf("%s-%s", strings.Repeat("a", 229), "df1f7590-test-namespace")))
				Expect(mig.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNameAnnotation, name))
				Expect(mig.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNamespaceAnnotation, testutils.TestNamespace))
				Expect(mig.Labels).ToNot(HaveKey(multiNamespaceStorageMigrationNameLabel))
				Expect(mig.Labels).ToNot(HaveKey(multiNamespaceStorageMigrationNamespaceLabel))

				Expect(reconciler.Client.Get(ctx, nn, multinsMigration)).To(Succeed())
				Expect(mig.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigrationUIDLabel, BeEquivalentTo(multinsMigration.UID)))
			})
		})

		Context("Cross-namespace parent lookup", func() {
			It("should correctly find parent in different namespace via annotations", func() {
				const crossNamespace = "cross-namespace-test"
				planName := "cross-ns-plan"
				migrationName := "cross-ns-migration"
				parentNamespace := testutils.TestNamespace

				By("Creating a second namespace for the child migration")
				testutils.CreateMigPlanNamespace(ctx, reconciler.Client, crossNamespace)
				defer testutils.DeleteMigPlanNamespace(ctx, reconciler.Client, crossNamespace)

				By("Creating multi-namespace plan and migration in parent namespace")
				multiPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      planName,
						Namespace: parentNamespace,
					},
					Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
						Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
							{
								Name: crossNamespace,
								VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
									VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
										{
											Name: "test-vm",
											TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
												{
													VolumeName:     "dv-disk",
													DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{},
												},
											},
										},
									},
								},
							},
						},
					},
				}
				multiMigration := &migrations.MultiNamespaceVirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{
						Name:      migrationName,
						Namespace: parentNamespace,
					},
					Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationSpec{
						MultiNamespaceVirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
							Name: planName,
						},
					},
				}
				Expect(reconciler.Client.Create(ctx, multiPlan)).To(Succeed())
				Expect(reconciler.Client.Create(ctx, multiMigration)).To(Succeed())

				By("Reconciling to add finalizer")
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: migrationName, Namespace: parentNamespace},
				})
				Expect(err).NotTo(HaveOccurred())

				By("Reconciling to create child migration in cross namespace")
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: migrationName, Namespace: parentNamespace},
				})
				Expect(err).NotTo(HaveOccurred())

				By("Verifying child migration exists in the cross namespace")
				childMigrationName := migrations.GetNamespacedPlanName(planName, crossNamespace)
				childMigration := &migrations.VirtualMachineStorageMigration{}
				Expect(reconciler.Client.Get(ctx, types.NamespacedName{
					Name:      childMigrationName,
					Namespace: crossNamespace,
				}, childMigration)).To(Succeed())

				By("Verifying child migration has correct annotations pointing to parent namespace")
				Expect(childMigration.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNameAnnotation, migrationName))
				Expect(childMigration.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNamespaceAnnotation, parentNamespace),
					"Child migration annotation should point to parent's namespace, not child's namespace")

				By("Verifying child migration has correct UID label")
				Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: migrationName, Namespace: parentNamespace}, multiMigration)).To(Succeed())
				Expect(childMigration.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigrationUIDLabel, string(multiMigration.UID)))

				By("Testing watch function correctly finds parent in different namespace")
				requests := reconciler.getVirtualMachineStorageMigrationsForMultiNamespaceStorageMigration(ctx, childMigration)
				Expect(requests).To(HaveLen(1))
				Expect(requests[0].NamespacedName).To(Equal(types.NamespacedName{
					Name:      migrationName,
					Namespace: parentNamespace,
				}), "Watch function should return parent migration in parent namespace")
			})
		})
	})
})

func createMultiNamespaceStorageMigration(name, planName string) *migrations.MultiNamespaceVirtualMachineStorageMigration {
	return &migrations.MultiNamespaceVirtualMachineStorageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testutils.TestNamespace,
		},
		Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationSpec{
			MultiNamespaceVirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
				Name: planName,
			},
		},
	}
}

func createMultiNamespaceStorageMigrationPlan(name string) *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan {
	return &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testutils.TestNamespace,
		},
		Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
			Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
				{
					Name: testutils.TestNamespace,
					VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
						VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
							{
								Name: "simple-vm",
								TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
									{
										VolumeName:     "dv-disk",
										DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
