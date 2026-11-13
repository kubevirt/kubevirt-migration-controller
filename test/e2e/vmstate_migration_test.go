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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/test/utils/console"
	cd "kubevirt.io/kubevirt-migration-controller/test/utils/containerdisk"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libdv"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmi"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmifact"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libwait"
	"kubevirt.io/kubevirt-migration-controller/test/utils/matcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This exercises the offline migration of the backend-storage (VMState) PVC that KubeVirt creates for
// a VM with a persistent vTPM. The VMState PVC is not in the VM's volume list, so we verify the
// controller clones it to the target storage class and KubeVirt adopts the clone on the next start.
//
// Needs a cluster with KubeVirt + CDI and at least two storage classes; it skips otherwise.
var _ = Describe("MigPlan VMState offline migration", func() {
	const (
		planName      = "e2e-vmstate-plan"
		migrationName = "e2e-vmstate-migration"
		volumeName    = "disk0"
		backendLabel  = "persistent-state-for"
	)

	var (
		namespace *corev1.Namespace
		defaultSC string
	)

	BeforeEach(func() {
		namespace, defaultSC = setupNamespaceAndStorageClassHelper("e2e-vmstate-mig-")
	})

	AfterEach(func() {
		_ = c.Delete(context.TODO(), &migrations.VirtualMachineStorageMigration{
			ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name}})
		_ = c.Delete(context.TODO(), &migrations.VirtualMachineStorageMigrationPlan{
			ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace.Name}})
		cleanupNamespaceHelper(namespace)
	})

	vmStatePVC := func(vmName string) *corev1.PersistentVolumeClaim {
		pvcs := &corev1.PersistentVolumeClaimList{}
		Expect(c.List(context.TODO(), pvcs,
			client.InNamespace(namespace.Name), client.MatchingLabels{backendLabel: vmName})).To(Succeed())
		if len(pvcs.Items) != 1 {
			return nil
		}
		return &pvcs.Items[0]
	}

	It("migrates a stopped persistent-vTPM VM's VMState PVC to the target storage class", func() {
		By("Picking a second storage class to migrate to")
		scList := &storagev1.StorageClassList{}
		Expect(c.List(context.TODO(), scList)).To(Succeed())
		targetSC := ""
		for i := range scList.Items {
			if scList.Items[i].Name != defaultSC {
				targetSC = scList.Items[i].Name
				break
			}
		}
		if targetSC == "" {
			Skip("needs at least two storage classes")
		}

		By("Creating a running cirros VM with a persistent vTPM")
		dv := libdv.NewDataVolume(
			libdv.WithNamespace(namespace.Name),
			libdv.WithRegistryURLSourceAndCustomCA(
				cd.DataVolumeImportUrlForContainerDisk(cd.ContainerDiskCirros), registryProxyCACertName),
			libdv.WithStorage(
				libdv.StorageWithStorageClass(defaultSC),
				libdv.StorageWithVolumeSize(cd.CirrosVolumeSize),
				libdv.StorageWithFilesystemVolumeMode(),
			),
		)
		vmi := libvmi.New(
			libvmi.WithNamespace(namespace.Name),
			libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
			libvmi.WithNetwork(virtv1.DefaultPodNetwork()),
			libvmi.WithMemoryRequest("128Mi"),
			libvmi.WithDataVolume(volumeName, dv.Name),
			libvmi.WithCloudInitNoCloud(libvmifact.WithDummyCloudForFastBoot()),
		)
		vmi.Spec.Domain.Devices.TPM = &virtv1.TPMDevice{Persistent: ptr.To(true)}
		vm := libvmi.NewVirtualMachine(vmi,
			libvmi.WithRunStrategy(virtv1.RunStrategyAlways),
			libvmi.WithDataVolumeTemplate(dv),
		)
		vm.Namespace = namespace.Name
		Expect(c.Create(context.Background(), vm)).To(Succeed())

		By("Waiting for the VM to be running")
		Eventually(matcher.ThisVM(vm, c), 360*time.Second, time.Second).Should(matcher.BeReady())
		runningVMI := &virtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: namespace.Name}}
		Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), runningVMI)).To(Succeed())
		libwait.WaitForSuccessfulVMIStart(runningVMI, c)
		Expect(console.LoginToCirros(runningVMI)).To(Succeed())

		By("Confirming KubeVirt created the VMState PVC and it is not already on the target")
		var sourceVMStatePVCName string
		Eventually(func(g Gomega) {
			pvc := vmStatePVC(vm.Name)
			g.Expect(pvc).NotTo(BeNil(), "exactly one VMState PVC should exist")
			g.Expect(ptr.Deref(pvc.Spec.StorageClassName, "")).NotTo(Equal(targetSC),
				"VMState PVC is already on the target storage class; test cannot prove a migration")
			sourceVMStatePVCName = pvc.Name
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("Stopping the VM")
		stopped := vm.DeepCopy()
		stopped.Spec.RunStrategy = ptr.To(virtv1.RunStrategyHalted)
		Expect(c.Patch(context.TODO(), stopped, client.MergeFrom(vm))).To(Succeed())
		Eventually(func() bool {
			return k8serrors.IsNotFound(
				c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), &virtv1.VirtualMachineInstance{}))
		}, 3*time.Minute, 5*time.Second).Should(BeTrue())

		By("Creating the migration plan and migration targeting the second storage class")
		plan := &migrations.VirtualMachineStorageMigrationPlan{
			ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace.Name},
			Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
				VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{{
					Name: vm.Name,
					TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{{
						VolumeName: volumeName,
						DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
							Name:             ptr.To("target-" + volumeName),
							StorageClassName: ptr.To(targetSC),
							AccessModes: []migrations.VirtualMachineStorageMigrationPlanAccessMode{
								migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce)},
							VolumeMode: ptr.To(corev1.PersistentVolumeFilesystem),
						},
					}},
				}},
			},
		}
		Expect(c.Create(context.TODO(), plan)).To(Succeed())

		By("Waiting for the plan to be ready")
		Eventually(func(g Gomega) {
			p := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace.Name}}
			g.Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(p), p)).To(Succeed())
			cond := p.Status.FindCondition(migrations.Ready)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionTrue), "plan Ready: %v", cond)
			g.Expect(p.Status.ReadyMigrations).NotTo(BeEmpty())
		}, 5*time.Minute, 5*time.Second).Should(Succeed())

		migration := &migrations.VirtualMachineStorageMigration{
			ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
			Spec: migrations.VirtualMachineStorageMigrationSpec{
				VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
					Name: planName, Namespace: namespace.Name},
			},
		}
		Expect(c.Create(context.TODO(), migration)).To(Succeed())

		By("Waiting for the migration to complete")
		Eventually(func(g Gomega) {
			m := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name}}
			g.Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(m), m)).To(Succeed())
			g.Expect(m.Status.Phase).To(Equal(migrations.Completed), "phase=%s", m.Status.Phase)
			g.Expect(m.Status.CompletedMigrations).To(ConsistOf(vm.Name))
		}, 10*time.Minute, 15*time.Second).Should(Succeed())

		By("Verifying exactly one VMState PVC exists and it is now on the target storage class")
		Eventually(func(g Gomega) {
			pvc := vmStatePVC(vm.Name)
			g.Expect(pvc).NotTo(BeNil(), "exactly one current VMState PVC should carry the label")
			g.Expect(ptr.Deref(pvc.Spec.StorageClassName, "")).To(Equal(targetSC))
			g.Expect(pvc.Name).NotTo(Equal(sourceVMStatePVCName), "label should have moved to the clone")
		}, 5*time.Minute, 10*time.Second).Should(Succeed())

		By("Restarting the VM and confirming it boots with the migrated VMState")
		current := &virtv1.VirtualMachine{}
		Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(vm), current)).To(Succeed())
		started := current.DeepCopy()
		started.Spec.RunStrategy = ptr.To(virtv1.RunStrategyAlways)
		Expect(c.Patch(context.TODO(), started, client.MergeFrom(current))).To(Succeed())
		Eventually(matcher.ThisVM(vm, c), 360*time.Second, time.Second).Should(matcher.BeReady())
		restartedVMI := &virtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: namespace.Name}}
		Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(restartedVMI), restartedVMI)).To(Succeed())
		libwait.WaitForSuccessfulVMIStart(restartedVMI, c)
		Expect(console.LoginToCirros(restartedVMI)).To(Succeed())
	})
})
