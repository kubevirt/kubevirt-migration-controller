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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	virtv1 "kubevirt.io/api/core/v1"
)

var _ = Describe("VMIExists", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(virtv1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns true when VMI exists", func() {
		vmi := &virtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vm", Namespace: "test-ns"},
			Status:     virtv1.VirtualMachineInstanceStatus{Phase: virtv1.Running},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vmi).Build()
		exists, err := VMIExists(ctx, c, "test-vm", "test-ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("returns false when VMI does not exist", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		exists, err := VMIExists(ctx, c, "test-vm", "test-ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
})

var _ = Describe("IsVMRunning", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(virtv1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns true when VMI exists and IsRunning is true", func() {
		vmi := &virtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vm", Namespace: "test-ns"},
			Status:     virtv1.VirtualMachineInstanceStatus{Phase: virtv1.Running},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vmi).Build()
		running, err := IsVMRunning(ctx, c, "test-vm", "test-ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeTrue())
	})

	It("returns false when VMI does not exist", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		running, err := IsVMRunning(ctx, c, "test-vm", "test-ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeFalse())
	})

	It("returns false when VMI exists but is not Running", func() {
		vmi := &virtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "test-vm", Namespace: "test-ns"},
			Status:     virtv1.VirtualMachineInstanceStatus{Phase: virtv1.Scheduled},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vmi).Build()
		running, err := IsVMRunning(ctx, c, "test-vm", "test-ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeFalse())
	})
})
