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
package testutils

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

const (
	TestNamespace     = "test-namespace"
	TestMigPlanName   = "test-migplan"
	TestSourcePVCName = "test-source-pvc"
	TestTargetPVCName = "test-target-pvc"
	TestVMName        = "test-vm"
	TestVMIMName      = "test-vmim"
	TestVolumeName    = "test-volume"
	TestNodeName      = "test-node"
)

func NewVirtualMachineStorageMigrationPlan(name string, vms ...*virtv1.VirtualMachine) *migrations.VirtualMachineStorageMigrationPlan {
	plan := &migrations.VirtualMachineStorageMigrationPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       migrations.VirtualMachineStorageMigrationPlanKind,
			APIVersion: migrations.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: TestNamespace,
		},
		Spec: migrations.VirtualMachineStorageMigrationPlanSpec{},
	}

	for _, vm := range vms {
		volumes := make([]migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC, 0)
		for _, volume := range vm.Spec.Template.Spec.Volumes {
			// pvcName := ""
			// if volume.PersistentVolumeClaim != nil {
			// 	pvcName = volume.PersistentVolumeClaim.ClaimName
			// } else if volume.DataVolume != nil {
			// 	pvcName = volume.DataVolume.Name
			// }
			// if pvcName == "" {
			// 	pvcName =
			// }
			volumes = append(volumes, migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
				VolumeName: volume.Name,
				DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
					Name: ptr.To(TestTargetPVCName),
				},
			})
		}
		plan.Spec.VirtualMachines = append(plan.Spec.VirtualMachines, migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
			Name:                vm.Name,
			TargetMigrationPVCs: volumes,
		})
	}
	return plan
}

func NewVirtualMachine(name, namespace, volumeName, pvcName string) *virtv1.VirtualMachine {
	return &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: virtv1.VirtualMachineSpec{
			Template: &virtv1.VirtualMachineInstanceTemplateSpec{
				Spec: virtv1.VirtualMachineInstanceSpec{
					Volumes: []virtv1.Volume{
						{
							Name: volumeName,
							VolumeSource: virtv1.VolumeSource{
								PersistentVolumeClaim: &virtv1.PersistentVolumeClaimVolumeSource{
									PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: pvcName,
									},
								},
							},
						},
					},
				},
			},
		},
		Status: virtv1.VirtualMachineStatus{
			Ready: true,
			Conditions: []virtv1.VirtualMachineCondition{
				{
					Type:   componenthelpers.StorageLiveMigratable,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

func NewVirtualMachineInstance(name, namespace string, podUID types.UID, nodeName string) *virtv1.VirtualMachineInstance {
	return &virtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: virtv1.VirtualMachineInstanceSpec{},
		Status: virtv1.VirtualMachineInstanceStatus{
			ActivePods: map[types.UID]string{
				podUID: nodeName,
			},
		},
	}
}

func NewPersistentVolumeClaim(name, namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}
