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

package storagemigplan

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("StorageMigPlan Controller envtests - with minimal real apiserver", func() {
	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      testutils.TestMigPlanName,
		Namespace: testutils.TestNamespace,
	}

	var (
		reconciler *StorageMigPlanReconciler
		migplan    *migrations.VirtualMachineStorageMigrationPlan
	)

	BeforeEach(func() {
		reconciler = &StorageMigPlanReconciler{
			Client:        k8sClient,
			Scheme:        scheme.Scheme,
			EventRecorder: record.NewFakeRecorder(10),
		}
		// Create a default storage class
		storageClass := testutils.NewDefaultStorageClass("test-storage-class")
		Expect(reconciler.Client.Create(ctx, storageClass)).To(Succeed())
		migplan = testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName)
		Expect(reconciler.Client.Create(ctx, migplan)).To(Succeed())
	})

	AfterEach(func() {
		if reconciler != nil {
			close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
			testutils.CleanupResources(ctx, reconciler.Client)
			reconciler = nil
		}
	})

	Context("When reconciling a migplan", func() {
		It("should properly handle refresh annotation", func() {
			By("Setting the refresh annotation")
			updated := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Annotations[RefreshStartTimeAnnotation]).To(BeEmpty())
			migplan.Annotations = map[string]string{
				RefreshStartTimeAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
			}
			Expect(k8sClient.Update(ctx, migplan)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			updated = &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Annotations[RefreshStartTimeAnnotation]).ToNot(BeEmpty())
			Expect(updated.Annotations[RefreshEndTimeAnnotation]).NotTo(BeEmpty())
		})

		It("should skip reconcile if plan cannot be found", func() {
			By("Deleting the plan")
			Expect(k8sClient.Delete(ctx, migplan)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
