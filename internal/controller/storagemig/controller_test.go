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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("StorageMigration Controller", func() {
	var (
		controllerReconciler *StorageMigrationReconciler
	)
	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      testutils.TestMigMigrationName,
		Namespace: testutils.TestNamespace,
	}

	BeforeEach(func() {
		controllerReconciler = &StorageMigrationReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	AfterEach(func() {
		if controllerReconciler != nil {
			CleanupResources(ctx, controllerReconciler.Client)
			controllerReconciler = nil
		}
	})

	Context("When reconciling a migmigration", func() {
		It("should mark the migration as blocked if the plan is not found", func() {
			migration := createMigration()
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.HasBlockerCondition()).To(BeTrue())
			Expect(migration.Status.Conditions.List).To(ContainElement(
				And(
					HaveField("Type", migrations.InvalidPlanRef),
					HaveField("Status", corev1.ConditionTrue),
				),
			), "Expected conditions differ from found")
		})

		It("should not reconcile if the migration is completed", func() {
			migration := createMigration()
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			migration.Status.Phase = migrations.Completed
			Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.Completed))
		})

		It("should not reconcile if the migration is not found", func() {
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When reconciling a migration that is completed", func() {
		It("should enforce retentionPolicy immutability with type-level and field-level validation", func() {
			key := types.NamespacedName{Name: "test-resource", Namespace: "default"}
			created := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						{
							Name: testutils.TestVMName,
							TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
								{
									VolumeName: testutils.TestVolumeName,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, created)).To(Succeed())
			existing := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())

			By("Attempting to change value from deleteSource to keepSource - field-level validation should fail")
			existing.Spec.RetentionPolicy = ptr.To(migrations.RetentionPolicyKeepSource)
			err := k8sClient.Update(ctx, existing)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("retentionPolicy"))
			existing = &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())
			Expect(*existing.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource),
				"Value should not have changed")

			By("Attempting to remove retentionPolicy (set to nil)")
			existing.Spec.RetentionPolicy = nil
			err = k8sClient.Update(ctx, existing)

			// With CRD defaulting, nil becomes keepSource, so this triggers field-level validation
			// The error will be about value change (deleteSource -> keepSource) not removal
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(
				ContainSubstring("retentionPolicy value cannot be changed"),
				ContainSubstring("retentionPolicy cannot be removed"),
			), "Either field-level or type-level validation should prevent the change")
			existing = &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())
			Expect(*existing.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource),
				"Value should still be deleteSource")
		})
	})
})

func createMigration() *migrations.VirtualMachineStorageMigration {
	return &migrations.VirtualMachineStorageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testutils.TestMigMigrationName,
			Namespace: testutils.TestNamespace,
		},
		Spec: migrations.VirtualMachineStorageMigrationSpec{
			VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
				Name: testutils.TestMigPlanName,
			},
		},
	}
}

func CleanupResources(ctx context.Context, client client.Client) {
	testutils.CleanupResources(ctx, client)
	cleanupMigrations(ctx, client)
}

func cleanupMigrations(ctx context.Context, client client.Client) {
	migrationList := &migrations.VirtualMachineStorageMigrationList{}
	Expect(client.List(ctx, migrationList)).To(Succeed())
	for _, migration := range migrationList.Items {
		Expect(client.Delete(ctx, &migration)).To(Succeed())
	}
}
