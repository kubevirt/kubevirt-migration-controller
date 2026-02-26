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
package multinamespacestoragemigplan

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("MultiNamespaceStorageMigPlan controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		var reconciler *MultiNamespaceStorageMigPlanReconciler

		BeforeEach(func() {
			reconciler = &MultiNamespaceStorageMigPlanReconciler{
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

		Context("Naming length constraints", func() {
			It("should respect naming length constraints for namespaced plans", func() {
				name := strings.Repeat("a", kvalidation.DNS1123SubdomainMaxLength)
				nn := types.NamespacedName{Name: name, Namespace: testutils.TestNamespace}
				multinsPlan := createMultiNamespaceStorageMigrationPlan(name)
				Expect(reconciler.Client.Create(ctx, multinsPlan)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nn,
				})
				Expect(err).NotTo(HaveOccurred())
				np := &migrations.VirtualMachineStorageMigrationPlan{}
				npnn := types.NamespacedName{Name: migrations.GetNamespacedPlanName(name, testutils.TestNamespace), Namespace: testutils.TestNamespace}
				Expect(reconciler.Client.Get(ctx, npnn, np)).To(Succeed())
				Expect(np.Name).To(HaveLen(kvalidation.DNS1123SubdomainMaxLength))
				Expect(np.Name).To(Equal(fmt.Sprintf("%s-%s", strings.Repeat("a", 229), "df1f7590-test-namespace")))
				Expect(np.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNameAnnotation, name))
				Expect(np.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNamespaceAnnotation, testutils.TestNamespace))
				Expect(np.Labels).ToNot(HaveKey(multiNamespaceStorageMigPlanNameLabel))
				Expect(np.Labels).ToNot(HaveKey(multiNamespaceStorageMigPlanNamespaceLabel))

				Expect(reconciler.Client.Get(ctx, nn, multinsPlan)).To(Succeed())
				Expect(np.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigPlanUIDLabel, BeEquivalentTo(multinsPlan.UID)))
			})
		})
	})
})

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

var _ = Describe("MultiNamespaceStorageMigPlan Controller", func() {
	var (
		reconciler *MultiNamespaceStorageMigPlanReconciler
	)

	ctx := context.Background()

	planName := "test-multi-plan"
	typeNamespacedName := types.NamespacedName{
		Name:      planName,
		Namespace: testutils.TestNamespace,
	}

	BeforeEach(func() {
		reconciler = &MultiNamespaceStorageMigPlanReconciler{
			Client:        k8sClient,
			Scheme:        scheme.Scheme,
			EventRecorder: record.NewFakeRecorder(10),
			Log:           logf.Log.WithName("multinamespace-migplan-test"),
		}
	})

	AfterEach(func() {
		if reconciler != nil {
			close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
			testutils.CleanupResources(ctx, reconciler.Client)
			reconciler = nil
		}
	})

	Context("RetentionPolicy", func() {
		It("should create namespaced plan with deleteSource when multinamespace plan has retentionPolicy deleteSource", func() {
			By("Creating a multinamespace plan with retentionPolicy deleteSource")
			basePlan := testutils.NewVirtualMachineStorageMigrationPlan(
				testutils.TestMigPlanName,
				testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName),
			)
			multiPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      planName,
					Namespace: testutils.TestNamespace,
				},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name:                                   testutils.TestNamespace,
							VirtualMachineStorageMigrationPlanSpec: basePlan.Spec.DeepCopy(),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, multiPlan)).To(Succeed())

			By("Reconciling the multinamespace plan")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the created namespaced plan has retentionPolicy deleteSource")
			childPlanName := migrations.GetNamespacedPlanName(planName, testutils.TestNamespace)
			childPlan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: childPlanName, Namespace: testutils.TestNamespace}, childPlan)).To(Succeed())
			Expect(childPlan.Spec.RetentionPolicy).NotTo(BeNil())
			Expect(*childPlan.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource))
		})

		It("Should allow setting the field for the first time, but not update/delete it", func() {
			key := types.NamespacedName{Name: "test-resource", Namespace: "default"}
			created := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name: testutils.TestNamespace,
							VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
								VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, created)).To(Succeed())
			existing := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())

			// Attempt to change the value
			existing.Spec.RetentionPolicy = ptr.To(migrations.RetentionPolicyKeepSource)
			err := k8sClient.Update(ctx, existing)

			Expect(err).To(HaveOccurred())
			// Verify the specific CEL error message from your marker
			Expect(err.Error()).To(ContainSubstring("retentionPolicy is immutable"))
			existing = &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())

			// Attempt to delete (set to nil)
			existing.Spec.RetentionPolicy = nil
			err = k8sClient.Update(ctx, existing)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("retentionPolicy is immutable"))
		})
	})
})
