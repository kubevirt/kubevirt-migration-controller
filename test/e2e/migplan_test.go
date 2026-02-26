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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/test/utils/console"
	cd "kubevirt.io/kubevirt-migration-controller/test/utils/containerdisk"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libdv"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmi"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmifact"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libwait"
	"kubevirt.io/kubevirt-migration-controller/test/utils/matcher"
)

var _ = Describe("MigPlan", func() {
	var (
		namespace *corev1.Namespace
	)

	BeforeEach(func() {
		By("Creating a new test namespace")
		namespaceName := "e2e-test-migplan-" + rand.String(6)
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
		}
		err := c.Create(context.TODO(), namespace, &client.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		By("Deleting the test migplan")
		Eventually(func() bool {
			migrationPlan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migrationPlan, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())

		By("Deleting the test namespace")
		Eventually(func() bool {
			err := c.Delete(context.TODO(), namespace, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())
	})

	It("plan should be marked as not ready when VM is missing", func() {
		plan := &migrations.VirtualMachineStorageMigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-plan",
				Namespace: namespace.Name,
			},
			Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
				VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
					{
						Name: "test-vm",
						TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
							{
								VolumeName: "test-volume",
								DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
									Name:             ptr.To("test-pvc"),
									StorageClassName: ptr.To("test-storage-class"),
									AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
									VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
								},
							},
						},
					},
				},
			},
		}
		err := c.Create(context.TODO(), plan, &client.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: namespace.Name},
			}
			err := c.Get(context.TODO(), client.ObjectKeyFromObject(plan), plan, &client.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			return plan.Status.HasCondition(migrations.PlanNotReady)
		}, 30*time.Second, time.Second).Should(BeFalse())
	})

	Context("offline migration", func() {
		const (
			planName      = "e2e-storage-mig-plan"
			migrationName = "e2e-storage-migration"
			volumeName    = "disk0"
		)

		var (
			namespace        *corev1.Namespace
			storageClassName string
		)

		copyProxyCA := func(namespace string) {
			By("Copying proxy CA to test namespace")
			ca := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: *registryProxyNamespace},
			}
			err := c.Get(context.TODO(), client.ObjectKeyFromObject(ca), ca, &client.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			newCa := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: namespace},
				Data:       ca.Data,
			}
			err = c.Create(context.TODO(), newCa, &client.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			By("Waiting for proxy CA to be copied to test namespace")
			Eventually(func() bool {
				ca := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: namespace},
				}
				err := c.Get(context.TODO(), client.ObjectKeyFromObject(ca), ca, &client.GetOptions{})
				return err == nil
			}, 10*time.Second, 1*time.Second).Should(BeTrue())
		}

		BeforeEach(func() {
			namespaceName := "e2e-storage-mig-" + rand.String(6)
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
			}
			By("Creating test namespace " + namespace.Name)
			err := c.Create(context.TODO(), namespace, &client.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Resolving default storage class")
			scList := &storagev1.StorageClassList{}
			err = c.List(context.TODO(), scList, &client.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			for i := range scList.Items {
				if scList.Items[i].Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
					storageClassName = scList.Items[i].Name
					break
				}
			}
			if storageClassName == "" && len(scList.Items) > 0 {
				storageClassName = scList.Items[0].Name
			}
			Expect(storageClassName).NotTo(BeEmpty(), "cluster must have at least one storage class")
			copyProxyCA(namespace.Name)
		})

		AfterEach(func() {
			By("Deleting migration if present")
			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migration, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("Deleting plan if present")
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace.Name},
			}
			err = c.Delete(context.TODO(), plan, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			By("Deleting test namespace")
			Eventually(func() bool {
				err := c.Delete(context.TODO(), namespace, &client.DeleteOptions{})
				if k8serrors.IsNotFound(err) {
					return true
				}
				Expect(err).NotTo(HaveOccurred())
				return false
			}, 60*time.Second, 2*time.Second).Should(BeTrue())
		})

		createDVSpec := func(sc, size string) *cdiv1.DataVolume {
			dv := libdv.NewDataVolume(
				libdv.WithNamespace(namespace.Name),
				libdv.WithRegistryURLSourceAndCustomCA(
					cd.DataVolumeImportUrlForContainerDisk(cd.ContainerDiskCirros), registryProxyCACertName),
				libdv.WithStorage(libdv.StorageWithStorageClass(sc),
					libdv.StorageWithVolumeSize(size),
					libdv.StorageWithFilesystemVolumeMode(),
				),
			)
			return dv
		}

		createVMWithDV := func(dv *cdiv1.DataVolume,
			runStrategy virtv1.VirtualMachineRunStrategy) *virtv1.VirtualMachine {
			vmi := libvmi.New(
				libvmi.WithNamespace(dv.Namespace),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(virtv1.DefaultPodNetwork()),
				libvmi.WithMemoryRequest("128Mi"),
				libvmi.WithDataVolume("disk0", dv.Name),
				libvmi.WithCloudInitNoCloud(libvmifact.WithDummyCloudForFastBoot()),
			)
			vm := libvmi.NewVirtualMachine(vmi,
				libvmi.WithRunStrategy(runStrategy),
				libvmi.WithDataVolumeTemplate(dv),
			)
			vm.Namespace = dv.Namespace
			err := c.Create(context.Background(), vm, &client.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			if runStrategy == virtv1.RunStrategyAlways || runStrategy == virtv1.RunStrategyRerunOnFailure {
				Eventually(matcher.ThisVM(vm, c), 360*time.Second, 1*time.Second).Should(matcher.BeReady())
				By(fmt.Sprintf("Waiting for VMI %s to be ready", vmi.Name))
				vmi = &virtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{Name: vmi.Name, Namespace: vmi.Namespace},
				}
				err = c.Get(context.TODO(), client.ObjectKeyFromObject(vmi), vmi, &client.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				libwait.WaitForSuccessfulVMIStart(vmi, c)
				By("logging in to the VMI")
				Expect(console.LoginToCirros(vmi)).To(Succeed())
			}

			return vm
		}

		// createPlanAndMigration creates the plan, waits for it to be ready, creates the migration, and returns.
		createPlanAndMigration := func(vmNames []string, targetPVCNames []string, namespace string, expectedReady int) {
			Expect(vmNames).To(HaveLen(len(targetPVCNames)))
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace},
				Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
					VirtualMachines: make([]migrations.VirtualMachineStorageMigrationPlanVirtualMachine, 0, len(vmNames)),
				},
			}
			for i, vmName := range vmNames {
				plan.Spec.VirtualMachines = append(plan.Spec.VirtualMachines,
					migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						Name: vmName,
						TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
							{
								VolumeName: volumeName,
								DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
									Name:             ptr.To(targetPVCNames[i]),
									StorageClassName: &storageClassName,
									AccessModes: []migrations.VirtualMachineStorageMigrationPlanAccessMode{
										migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce)},
									VolumeMode: ptr.To(corev1.PersistentVolumeMode("Filesystem")),
								},
							},
						},
					})
			}
			err := c.Create(context.TODO(), plan, &client.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for plan to have Ready condition and expected ReadyMigrations")
			Eventually(func(g Gomega) {
				p := &migrations.VirtualMachineStorageMigrationPlan{
					ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace},
				}
				getErr := c.Get(context.TODO(), client.ObjectKeyFromObject(p), p, &client.GetOptions{})
				g.Expect(getErr).NotTo(HaveOccurred())
				c := p.Status.FindCondition(migrations.Ready)
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(corev1.ConditionTrue), "plan Ready condition: %s", c.Message)
				g.Expect(len(p.Status.ReadyMigrations)).To(BeNumerically(">=", expectedReady),
					"ReadyMigrations: %d", len(p.Status.ReadyMigrations))
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace},
				Spec: migrations.VirtualMachineStorageMigrationSpec{
					VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
						Name:      planName,
						Namespace: namespace,
					},
				},
			}
			Expect(c.Create(context.TODO(), migration, &client.CreateOptions{})).To(Succeed())
		}

		waitMigrationCompleted := func(expectedVMNames []string, namespace string) {
			By("Waiting for migration to complete with all VMs in CompletedMigrations")
			Eventually(func(g Gomega) {
				m := &migrations.VirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace},
				}
				Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(m), m, &client.GetOptions{})).To(Succeed())
				g.Expect(m.Status.Phase).To(Equal(migrations.Completed), "phase=%s", m.Status.Phase)
				g.Expect(m.Status.CompletedMigrations).To(ConsistOf(expectedVMNames), "completed=%v", m.Status.CompletedMigrations)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
		}

		verifyRunningVM := func(vm *virtv1.VirtualMachine, targetPVCName string) {
			By("Checking the running VM is using the new PVC")
			runningVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVM), runningVM, &client.GetOptions{})).To(Succeed())
			Expect(runningVM.Spec.DataVolumeTemplates[0].Name).To(Equal(targetPVCName))
			Expect(runningVM.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(Equal(targetPVCName))
			By("Checking the running VM is still running, by logging in to it")
			runningVMI := &virtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), runningVMI, &client.GetOptions{})).To(Succeed())
			Expect(console.LoginToCirros(runningVMI)).To(Succeed())
		}

		verifyOfflineVM := func(vm *virtv1.VirtualMachine, targetPVCName string) {
			By("Checking the offline VM is using the new PVC")
			offlineVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(offlineVM), offlineVM, &client.GetOptions{})).To(Succeed())
			Expect(offlineVM.Spec.DataVolumeTemplates[0].Name).To(Equal(targetPVCName))
			Expect(offlineVM.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(Equal(targetPVCName))
		}

		It("should successfully migrate a plan with only offline VMs", func() {
			target1, target2 := "target-pvc-1", "target-pvc-2"

			dv1 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			dv2 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			vm1 := createVMWithDV(dv1, virtv1.RunStrategyHalted)
			vm2 := createVMWithDV(dv2, virtv1.RunStrategyHalted)

			createPlanAndMigration([]string{vm1.Name, vm2.Name}, []string{target1, target2}, namespace.Name, 2)
			waitMigrationCompleted([]string{vm1.Name, vm2.Name}, namespace.Name)
			verifyOfflineVM(vm1, target1)
			verifyOfflineVM(vm2, target2)
		})

		It("should successfully migrate a plan with one running and one offline VM", func() {
			targetRunning, targetOffline := "target-running", "target-offline"

			dv1 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			dv2 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			vmRunning := createVMWithDV(dv1, virtv1.RunStrategyAlways)
			vmOffline := createVMWithDV(dv2, virtv1.RunStrategyHalted)

			createPlanAndMigration([]string{vmRunning.Name, vmOffline.Name},
				[]string{targetRunning, targetOffline}, namespace.Name, 2)
			waitMigrationCompleted([]string{vmRunning.Name, vmOffline.Name}, namespace.Name)
			verifyRunningVM(vmRunning, targetRunning)
			verifyOfflineVM(vmOffline, targetOffline)
		})
	})

})
