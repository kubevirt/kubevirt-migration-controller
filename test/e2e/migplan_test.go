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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/internal/controller/storagemigplan"
)

var _ = Describe("MigPlan", func() {
	var (
		namespace string
	)

	BeforeEach(func() {
		By("Creating a new test namespace")
		namespace = "e2e-test-migplan-" + rand.String(6)
		_, err := kcs.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		By("Deleting the test migplan")
		Eventually(func() bool {
			err := mcs.MigrationcontrollerV1alpha1().VirtualMachineStorageMigrationPlans(namespace).Delete(context.TODO(),
				"test-plan", metav1.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())

		By("Deleting the test namespace")
		Eventually(func() bool {
			err := kcs.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
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
				Namespace: namespace,
			},
			Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
				VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
					{
						Name: "test-vm",
						TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
							{
								VolumeName: "test-volume",
								DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
									Name:             ptr.To[string]("test-pvc"),
									StorageClassName: ptr.To[string]("test-storage-class"),
									AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
									VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
								},
							},
						},
					},
				},
			},
		}
		_, err := mcs.MigrationcontrollerV1alpha1().VirtualMachineStorageMigrationPlans(namespace).Create(context.TODO(),
			plan, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			plan, err := mcs.MigrationcontrollerV1alpha1().VirtualMachineStorageMigrationPlans(namespace).Get(context.TODO(),
				"test-plan", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			return plan.Status.HasCriticalCondition(storagemigplan.StorageMigrationNotPossibleType)
		}, 30*time.Second, time.Second).Should(BeTrue())
	})
})
