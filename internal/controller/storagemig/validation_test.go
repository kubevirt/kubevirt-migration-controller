package storagemig

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("StorageMigration Validation", func() {
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

	Context("When validating a storage migration", func() {
		It("should return an error condition if the migration is not owned by a plan, because of mismatching uids", func() {
			migration := createMigration(testutils.TestMigMigrationName)
			migration.Spec.VirtualMachineStorageMigrationPlanRef.UID = "123"
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())
			migplan := testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName, testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName))
			Expect(k8sClient.Create(ctx, migplan)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.HasBlockerCondition()).To(BeTrue())
			Expect(migration.Status.Conditions.List).To(ContainElement(
				And(
					HaveField("Type", InvalidPlanRef),
					HaveField("Status", corev1.ConditionTrue),
					HaveField("Reason", NotFound),
					HaveField("Message", "migration is not owned by the plan test-migplan, uid mismatch"),
				),
			))
			By("fixing the UID mismatch")
			migration.Spec.VirtualMachineStorageMigrationPlanRef.UID = migplan.UID
			Expect(k8sClient.Update(ctx, migration)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.HasBlockerCondition()).To(BeFalse())
			Expect(migration.Status.Conditions.List).NotTo(ContainElement(
				And(
					HaveField("Type", InvalidPlanRef),
					HaveField("Status", corev1.ConditionTrue),
					HaveField("Reason", migrations.NotFound),
					HaveField("Message", "migration is not owned by the plan test-migplan, uid mismatch"),
				),
			))
		})

		It("should return an error condition if the migration plan is has critical conditions", func() {
			migration := createMigration(testutils.TestMigMigrationName)
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())
			migplan := testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName, testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName))
			Expect(k8sClient.Create(ctx, migplan)).To(Succeed())
			migplan.Status.SetCondition(migrations.Condition{
				Type:     PlanNotReady,
				Status:   corev1.ConditionTrue,
				Category: migrations.Critical,
				Message:  "plan has critical conditions",
			})
			Expect(k8sClient.Status().Update(ctx, migplan)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.HasBlockerCondition()).To(BeTrue())
			Expect(migration.Status.Conditions.List).To(ContainElement(
				And(
					HaveField("Type", PlanNotReady),
					HaveField("Status", corev1.ConditionTrue),
					HaveField("Reason", migrations.NotReady),
					HaveField("Message", "The referenced `virtualMachineStorageMigrationPlanRef` has critical conditions, subject: test-namespace/test-migplan"),
				),
			))
			By("fixing the plan conditions")
			migplan.Status.DeleteCondition(PlanNotReady)
			Expect(k8sClient.Status().Update(ctx, migplan)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.HasBlockerCondition()).To(BeFalse())
			Expect(migration.Status.Conditions.List).NotTo(ContainElement(
				And(
					HaveField("Type", PlanNotReady),
					HaveField("Status", corev1.ConditionTrue),
					HaveField("Reason", migrations.NotReady),
					HaveField("Message", "The referenced `virtualMachineStorageMigrationPlanRef` has critical conditions, subject: test-namespace/test-migplan"),
				),
			))
		})
	})
})
