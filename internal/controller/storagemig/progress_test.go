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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("parseDataVolumeProgress", func() {
	DescribeTable("parses DataVolume progress",
		func(dv *cdiv1.DataVolume, expectedPercent float64, expectedOK bool) {
			percent, ok := parseDataVolumeProgress(dv)
			Expect(ok).To(Equal(expectedOK))
			if expectedOK {
				Expect(percent).To(Equal(expectedPercent))
			}
		},
		Entry("Succeeded is 100", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.Succeeded, Progress: "N/A"},
		}, 100.0, true),
		Entry("WaitForFirstConsumer is unavailable", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.WaitForFirstConsumer, Progress: "0.00%"},
		}, 0.0, false),
		Entry("percentage with suffix", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.CloneInProgress, Progress: "48.12%"},
		}, 48.12, true),
		Entry("N/A is unavailable", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.CloneInProgress, Progress: "N/A"},
		}, 0.0, false),
		Entry("empty progress is unavailable", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.CloneInProgress},
		}, 0.0, false),
		Entry("unparseable progress is unavailable", &cdiv1.DataVolume{
			Status: cdiv1.DataVolumeStatus{Phase: cdiv1.CloneInProgress, Progress: "abc"},
		}, 0.0, false),
	)
})

var _ = Describe("weightedAverageProgress", func() {
	DescribeTable("calculates size-weighted average",
		func(samples []progressSample, expected string) {
			Expect(weightedAverageProgress(samples)).To(Equal(expected))
		},
		Entry("empty samples", nil, ""),
		Entry("single sample", []progressSample{{percent: 48.12, weight: 1}}, "48.12"),
		Entry("size-weighted", []progressSample{
			{percent: 0, weight: quantityValue("10Gi")},
			{percent: 100, weight: quantityValue("90Gi")},
		}, "90.00"),
		Entry("non-positive weight treated as 1", []progressSample{
			{percent: 50, weight: 0},
			{percent: 100, weight: -1},
		}, "75.00"),
	)
})

var _ = Describe("planSourcePVCStorageWeight", func() {
	DescribeTable("returns storage weight for a plan source PVC",
		func(planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, index int, expected int64) {
			Expect(planSourcePVCStorageWeight(planVM, index)).To(Equal(expected))
		},
		Entry("nil planVM", nil, 0, int64(1)),
		Entry("index out of range", &migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}, 0, int64(1)),
		Entry("source PVC storage request", func() *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
			pvc := testutils.NewPersistentVolumeClaim("src", testutils.TestNamespace)
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("10Gi")
			return &migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
					{SourcePVC: *pvc},
				},
			}
		}(), 0, quantityValue("10Gi")),
	)
})

func quantityValue(s string) int64 {
	q := resource.MustParse(s)
	return q.Value()
}

var _ = Describe("getTargetDataVolume", func() {
	AfterEach(func() {
		CleanupResources(ctx, k8sClient)
	})

	DescribeTable("looks up a target DataVolume",
		func(create bool, name string, expectFound bool) {
			if create {
				dv := &cdiv1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testutils.TestNamespace},
					Spec: cdiv1.DataVolumeSpec{
						Source: &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
						Storage: &cdiv1.StorageSpec{
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, dv)).To(Succeed())
			}

			t := &Task{
				Client: k8sClient,
				Owner:  &migrations.VirtualMachineStorageMigration{ObjectMeta: metav1.ObjectMeta{Namespace: testutils.TestNamespace}},
			}
			got, err := t.getTargetDataVolume(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			if expectFound {
				Expect(got).NotTo(BeNil())
				Expect(got.Name).To(Equal(name))
			} else {
				Expect(got).To(BeNil())
			}
		},
		Entry("returns nil when DataVolume does not exist", false, "missing-dv", false),
		Entry("returns the DataVolume when it exists", true, testTargetDV, true),
	)
})

type offlineProgressDV struct {
	name     string
	size     string
	phase    cdiv1.DataVolumePhase
	progress cdiv1.DataVolumeProgress
}

type offlineProgressSource struct {
	name string
	size string
}

var _ = Describe("getOfflineMigrationProgress", func() {
	const (
		dvSmall   = "dv-small"
		dvLarge   = "dv-large"
		dvMissing = "dv-missing"
		dvNoSize  = "dv-no-size"
	)

	AfterEach(func() {
		CleanupResources(ctx, k8sClient)
	})

	newTask := func(
		targets []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC,
		sources []migrations.VirtualMachineStorageMigrationPlanSourcePVC,
	) *Task {
		return &Task{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Log:    logf.Log.WithName("offline-progress-test"),
			Owner:  &migrations.VirtualMachineStorageMigration{ObjectMeta: metav1.ObjectMeta{Namespace: testutils.TestNamespace}},
			Plan: &migrations.VirtualMachineStorageMigrationPlan{
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
								Name:                testVM,
								TargetMigrationPVCs: targets,
							},
							SourcePVCs: sources,
						},
					},
				},
			},
		}
	}

	createDV := func(name, size string, phase cdiv1.DataVolumePhase, progress cdiv1.DataVolumeProgress) {
		storage := &cdiv1.StorageSpec{}
		if size != "" {
			storage.Resources = corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			}
		}
		dv := &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testutils.TestNamespace},
			Spec: cdiv1.DataVolumeSpec{
				Source:  &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
				Storage: storage,
			},
		}
		Expect(k8sClient.Create(ctx, dv)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testutils.TestNamespace, Name: name}, dv)).To(Succeed())
		dv.Status.Phase = phase
		dv.Status.Progress = progress
		Expect(k8sClient.Status().Update(ctx, dv)).To(Succeed())
	}

	source := func(name, size string) migrations.VirtualMachineStorageMigrationPlanSourcePVC {
		pvc := testutils.NewPersistentVolumeClaim(name, testutils.TestNamespace)
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse(size)
		return migrations.VirtualMachineStorageMigrationPlanSourcePVC{
			Name:      name,
			Namespace: testutils.TestNamespace,
			SourcePVC: *pvc,
		}
	}

	target := func(name *string) migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC {
		return migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
			DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{Name: name},
		}
	}

	DescribeTable("reports offline migration progress from target DataVolumes",
		func(dvs []offlineProgressDV, targetNames []*string, sources []offlineProgressSource, vmName, expected string) {
			for _, dv := range dvs {
				createDV(dv.name, dv.size, dv.phase, dv.progress)
			}
			targets := make([]migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC, 0, len(targetNames))
			for _, name := range targetNames {
				targets = append(targets, target(name))
			}
			var planSources []migrations.VirtualMachineStorageMigrationPlanSourcePVC
			for _, s := range sources {
				planSources = append(planSources, source(s.name, s.size))
			}
			progress, err := newTask(targets, planSources).getOfflineMigrationProgress(ctx, vmName)
			Expect(err).NotTo(HaveOccurred())
			Expect(progress).To(Equal(expected))
		},
		Entry("returns progress from a single DV percentage",
			[]offlineProgressDV{{name: testTargetDV, size: "1Gi", phase: cdiv1.CloneInProgress, progress: "48.12%"}},
			[]*string{ptr.To(testTargetDV)},
			nil,
			testVM,
			"48.12",
		),
		Entry("returns 100.00 when DV is Succeeded",
			[]offlineProgressDV{{name: testTargetDV, size: "1Gi", phase: cdiv1.Succeeded, progress: "N/A"}},
			[]*string{ptr.To(testTargetDV)},
			nil,
			testVM,
			"100.00",
		),
		Entry("returns empty for WaitForFirstConsumer",
			[]offlineProgressDV{{name: testTargetDV, size: "1Gi", phase: cdiv1.WaitForFirstConsumer, progress: "0.00%"}},
			[]*string{ptr.To(testTargetDV)},
			nil,
			testVM,
			"",
		),
		Entry("returns empty when all DVs report N/A",
			[]offlineProgressDV{{name: testTargetDV, size: "1Gi", phase: cdiv1.CloneInProgress, progress: "N/A"}},
			[]*string{ptr.To(testTargetDV)},
			nil,
			testVM,
			"",
		),
		Entry("returns size-weighted average across multiple DVs",
			[]offlineProgressDV{
				{name: dvSmall, size: "10Gi", phase: cdiv1.CloneInProgress, progress: "0.00%"},
				{name: dvLarge, size: "90Gi", phase: cdiv1.Succeeded, progress: "N/A"},
			},
			[]*string{ptr.To(dvSmall), ptr.To(dvLarge)},
			nil,
			testVM,
			"90.00",
		),
		Entry("returns 0.00 when target DataVolume is missing",
			nil,
			[]*string{ptr.To(dvMissing)},
			nil,
			testVM,
			"0.00",
		),
		Entry("returns empty when plan VM is not found",
			nil,
			[]*string{ptr.To(testTargetDV)},
			nil,
			"unknown-vm",
			"",
		),
		Entry("returns empty when plan VM has no target PVCs",
			nil,
			nil,
			nil,
			testVM,
			"",
		),
		Entry("counts nil DestinationPVC name as 0% with source weight",
			[]offlineProgressDV{{name: testTargetDV, size: "90Gi", phase: cdiv1.CloneInProgress, progress: "100.00%"}},
			[]*string{nil, ptr.To(testTargetDV)},
			[]offlineProgressSource{{name: "src-nil", size: "10Gi"}, {name: "src-target", size: "90Gi"}},
			testVM,
			"90.00",
		),
		Entry("averages missing DV as 0 with an in-progress DV",
			[]offlineProgressDV{{name: dvLarge, size: "90Gi", phase: cdiv1.CloneInProgress, progress: "100.00%"}},
			[]*string{ptr.To(dvMissing), ptr.To(dvLarge)},
			[]offlineProgressSource{{name: "src-missing", size: "10Gi"}, {name: "src-large", size: "90Gi"}},
			testVM,
			"90.00",
		),
		Entry("falls back to source PVC size when DV has no storage request",
			[]offlineProgressDV{
				{name: dvNoSize, size: "", phase: cdiv1.CloneInProgress, progress: "0.00%"},
				{name: dvLarge, size: "90Gi", phase: cdiv1.Succeeded, progress: "N/A"},
			},
			[]*string{ptr.To(dvNoSize), ptr.To(dvLarge)},
			[]offlineProgressSource{{name: "src-no-size", size: "10Gi"}, {name: "src-large", size: "90Gi"}},
			testVM,
			"90.00",
		),
	)
})
