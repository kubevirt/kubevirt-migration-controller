package testutils

import (
	"context"

	. "github.com/onsi/gomega"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateKubeVirtNamespace(ctx context.Context, client client.Client, namespace string) {
	kvNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}
	Expect(client.Create(ctx, kvNamespace)).To(Succeed())
}

func CreateMigPlanNamespace(ctx context.Context, client client.Client, namespace string) {
	migplanNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}
	Expect(client.Create(ctx, migplanNamespace)).To(Succeed())
}

func DeleteKubeVirtNamespace(ctx context.Context, client client.Client, namespace string) {
	Expect(client.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
}

func DeleteMigPlanNamespace(ctx context.Context, client client.Client, namespace string) {
	Expect(client.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
}

func CleanupResources(ctx context.Context, client client.Client) {
	cleanupKubeVirt(ctx, client)
	cleanupVirtualMachineStorageMigrationPlan(ctx, client)
	cleanupVirtualMachineStorageMigration(ctx, client)
	cleanupPVCs(ctx, client)
	cleanupStorageClasses(ctx, client)
}

func cleanupKubeVirt(ctx context.Context, client client.Client) {
	KubeVirtList := &virtv1.KubeVirtList{}
	Expect(client.List(ctx, KubeVirtList)).To(Succeed())
	for _, kv := range KubeVirtList.Items {
		Expect(client.Delete(ctx, &kv)).To(Succeed())
	}
	vmList := &virtv1.VirtualMachineList{}
	Expect(client.List(ctx, vmList)).To(Succeed())
	for _, vm := range vmList.Items {
		Expect(client.Delete(ctx, &vm)).To(Succeed())
	}
}

func cleanupVirtualMachineStorageMigrationPlan(ctx context.Context, client client.Client) {
	vmStorageMigrationPlanList := &migrations.VirtualMachineStorageMigrationPlanList{}
	Expect(client.List(ctx, vmStorageMigrationPlanList)).To(Succeed())
	for _, migPlan := range vmStorageMigrationPlanList.Items {
		Expect(client.Delete(ctx, &migPlan)).To(Succeed())
	}
}

func cleanupVirtualMachineStorageMigration(ctx context.Context, client client.Client) {
	vmStorageMigrationList := &migrations.VirtualMachineStorageMigrationList{}
	Expect(client.List(ctx, vmStorageMigrationList)).To(Succeed())
	for _, mig := range vmStorageMigrationList.Items {
		mig.Finalizers = []string{}
		Expect(client.Update(ctx, &mig)).To(Succeed())
		Expect(client.Delete(ctx, &mig)).To(Succeed())
	}
}

func cleanupPVCs(ctx context.Context, client client.Client) {
	PVCList := &corev1.PersistentVolumeClaimList{}
	Expect(client.List(ctx, PVCList)).To(Succeed())
	for _, pvc := range PVCList.Items {
		pvc.Finalizers = []string{}
		Expect(client.Update(ctx, &pvc)).To(Succeed())
		Expect(client.Delete(ctx, &pvc)).To(Succeed())
	}
}

func cleanupStorageClasses(ctx context.Context, client client.Client) {
	StorageClassList := &storagev1.StorageClassList{}
	Expect(client.List(ctx, StorageClassList)).To(Succeed())
	for _, storageClass := range StorageClassList.Items {
		Expect(client.Delete(ctx, &storageClass)).To(Succeed())
	}
}

func NewDefaultStorageClass(name string) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				componenthelpers.DefaultK8sStorageClass: "true",
			},
		},
		Provisioner: "migration.kubevirt.io/test-storage",
	}
}

func NewVirtDefaultStorageClass(name string) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				componenthelpers.DefaultVirtStorageClass: "true",
			},
		},
		Provisioner: "migration.kubevirt.io/test-storage",
	}
}
