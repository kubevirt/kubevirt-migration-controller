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
package componenthelpers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func vmWithTemplate() *virtv1.VirtualMachine {
	return &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "vm", Namespace: "ns"},
		Spec: virtv1.VirtualMachineSpec{
			Template: &virtv1.VirtualMachineInstanceTemplateSpec{
				Spec: virtv1.VirtualMachineInstanceSpec{},
			},
		},
	}
}

var _ = Describe("VMNeedsBackendStorage", func() {
	It("returns false for a nil VM", func() {
		Expect(VMNeedsBackendStorage(nil)).To(BeFalse())
	})

	It("returns false for a VM with no TPM and no EFI", func() {
		Expect(VMNeedsBackendStorage(vmWithTemplate())).To(BeFalse())
	})

	It("returns false for a non-persistent TPM", func() {
		vm := vmWithTemplate()
		vm.Spec.Template.Spec.Domain.Devices.TPM = &virtv1.TPMDevice{}
		Expect(VMNeedsBackendStorage(vm)).To(BeFalse())
	})

	It("returns true for a persistent TPM", func() {
		vm := vmWithTemplate()
		vm.Spec.Template.Spec.Domain.Devices.TPM = &virtv1.TPMDevice{Persistent: ptr.To(true)}
		Expect(VMNeedsBackendStorage(vm)).To(BeTrue())
	})

	It("returns false for a persistent TPM that is explicitly disabled", func() {
		vm := vmWithTemplate()
		vm.Spec.Template.Spec.Domain.Devices.TPM = &virtv1.TPMDevice{
			Enabled:    ptr.To(false),
			Persistent: ptr.To(true),
		}
		Expect(VMNeedsBackendStorage(vm)).To(BeFalse())
	})

	It("returns true for persistent EFI", func() {
		vm := vmWithTemplate()
		vm.Spec.Template.Spec.Domain.Firmware = &virtv1.Firmware{
			Bootloader: &virtv1.Bootloader{
				EFI: &virtv1.EFI{Persistent: ptr.To(true)},
			},
		}
		Expect(VMNeedsBackendStorage(vm)).To(BeTrue())
	})

	It("returns false for non-persistent EFI", func() {
		vm := vmWithTemplate()
		vm.Spec.Template.Spec.Domain.Firmware = &virtv1.Firmware{
			Bootloader: &virtv1.Bootloader{
				EFI: &virtv1.EFI{Persistent: ptr.To(false)},
			},
		}
		Expect(VMNeedsBackendStorage(vm)).To(BeFalse())
	})

	It("returns true when CBT is enabled", func() {
		vm := vmWithTemplate()
		vm.Status.ChangedBlockTracking = &virtv1.ChangedBlockTrackingStatus{State: virtv1.ChangedBlockTrackingEnabled}
		Expect(VMNeedsBackendStorage(vm)).To(BeTrue())
	})
})

var _ = Describe("GetBackendStoragePVC", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns the PVC labelled for the VM", func() {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      BackendStoragePVCLabel + "-test-vm",
				Namespace: "ns",
				Labels:    map[string]string{BackendStoragePVCLabel: "test-vm"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc).Build()
		got, err := GetBackendStoragePVC(ctx, c, "test-vm", "ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got.Name).To(Equal(BackendStoragePVCLabel + "-test-vm"))
	})

	It("returns nil when no backend-storage PVC exists for the VM", func() {
		other := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      BackendStoragePVCLabel + "-other-vm",
				Namespace: "ns",
				Labels:    map[string]string{BackendStoragePVCLabel: "other-vm"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(other).Build()
		got, err := GetBackendStoragePVC(ctx, c, "test-vm", "ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("GetVMStateStorageClass", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(virtv1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns the configured vmStateStorageClass", func() {
		kv := &virtv1.KubeVirt{
			ObjectMeta: metav1.ObjectMeta{Name: "kubevirt", Namespace: "kubevirt"},
			Spec: virtv1.KubeVirtSpec{
				Configuration: virtv1.KubeVirtConfiguration{VMStateStorageClass: "vmstate-sc"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kv).Build()
		sc, err := GetVMStateStorageClass(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc).To(Equal("vmstate-sc"))
	})

	It("returns empty string when vmStateStorageClass is unset", func() {
		kv := &virtv1.KubeVirt{
			ObjectMeta: metav1.ObjectMeta{Name: "kubevirt", Namespace: "kubevirt"},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kv).Build()
		sc, err := GetVMStateStorageClass(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc).To(BeEmpty())
	})

	It("returns empty string when there is no KubeVirt CR", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		sc, err := GetVMStateStorageClass(ctx, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc).To(BeEmpty())
	})
})
