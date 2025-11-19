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
	"context"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

// Requeue
const (
	FastReQ = time.Millisecond * 100
	PollReQ = time.Second * 3
	NoReQ   = time.Duration(0)

	virtLauncherPodLabelSelectorKey   = "kubevirt.io"
	virtLauncherPodLabelSelectorValue = "virt-launcher"
)

type Task struct {
	Config        *rest.Config
	Scheme        *runtime.Scheme
	Log           logr.Logger
	Client        k8sclient.Client
	Owner         *migrations.VirtualMachineStorageMigration
	Plan          *migrations.VirtualMachineStorageMigrationPlan
	Requeue       time.Duration
	Errors        []string
	PrometheusAPI prometheusv1.API
	PromQuery     func(ctx context.Context, query string, ts time.Time, opts ...prometheusv1.Option) (model.Value, prometheusv1.Warnings, error)
}

// Run the task.
// Each call will:
//  1. Run the current phase.
//  2. Update the phase to the next phase.
//  3. Set the Requeue (as appropriate).
//  4. Return.
func (t *Task) Run(ctx context.Context) error {
	// Set stage, phase, phase description, migplan name
	t.Requeue = NoReQ

	t.init()
	log := t.Log

	log.V(5).Info("Running task.Run", "phase", t.Owner.Status.Phase)
	// Run the current phase.
	switch t.Owner.Status.Phase {
	case migrations.Started:
		log.V(5).Info("Processing Started phase")
		// Set finalizer on migration
		t.Owner.AddFinalizer(migrations.VirtualMachineStorageMigrationFinalizer, t.Log)
		t.Owner.Status.Phase = migrations.RefreshStorageMigrationPlan
	case migrations.RefreshStorageMigrationPlan:
		log.V(5).Info("Processing RefreshStorageMigrationPlan phase")
		if err := t.refreshReadyVirtualMachines(ctx); err != nil {
			return err
		}
		t.Owner.Status.Phase = migrations.WaitForStorageMigrationPlanRefreshCompletion
	case migrations.WaitForStorageMigrationPlanRefreshCompletion:
		log.V(5).Info("Processing WaitForStorageMigrationPlanRefreshCompletion phase")
		if completed, err := t.refreshCompletedVirtualMachines(ctx); err != nil {
			return err
		} else if !completed {
			return nil
		}
		t.Owner.Status.Phase = migrations.BeginLiveMigration
	case migrations.BeginLiveMigration:
		log.V(5).Info("Processing BeginLiveMigration phase", "readyMigrations", len(t.Plan.Status.ReadyMigrations))
		log.V(5).Info("Processing BeginLiveMigration phase", "inProgressMigrations", len(t.Plan.Status.InProgressMigrations))
		checkMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
		checkMigrations = append(checkMigrations, t.Plan.Status.ReadyMigrations...)
		checkMigrations = append(checkMigrations, t.Plan.Status.InProgressMigrations...)
		for _, vm := range checkMigrations {
			if can, err := t.canVMStorageMigrate(ctx, vm.Name); err != nil {
				return err
			} else if !can {
				t.Log.V(3).Info("VM cannot storage migrate", "vm", vm.Name)
				continue
			}
			if err := t.liveMigrateVM(ctx, vm); err != nil {
				return err
			}
			t.Log.V(3).Info("VM migration is running", "vm", vm.Name)
			t.Owner.Status.RunningMigrations = append(t.Owner.Status.RunningMigrations, migrations.RunningVirtualMachineMigration{
				Name: vm.Name,
			})
		}
		t.Owner.Status.Phase = migrations.WaitForLiveMigrationToComplete
	case migrations.WaitForLiveMigrationToComplete:
		log.V(5).Info("Processing WaitForLiveMigrationToComplete phase", "runningMigrations", len(t.Owner.Status.RunningMigrations))
		runningMigrations := make([]migrations.RunningVirtualMachineMigration, 0)
		for _, vm := range t.Owner.Status.RunningMigrations {
			if ok, err := t.isLiveMigrationCompleted(ctx, vm.Name); err != nil {
				return err
			} else if !ok {
				runningMigrations = append(runningMigrations, vm)
				progress, err := t.getLastObservedProgressPercent(ctx, vm.Name, t.Owner.Namespace)
				if err != nil {
					return err
				}
				if progress != "" {
					vm.Progress = progress
				}
				continue
			}
			t.Owner.Status.CompletedMigrations = append(t.Owner.Status.CompletedMigrations, vm.Name)
		}
		t.Owner.Status.RunningMigrations = runningMigrations
		if len(runningMigrations) == 0 {
			t.Owner.Status.Phase = migrations.CleanupMigrationResources
		}
		t.Requeue = PollReQ
	case migrations.CleanupMigrationResources:
		if allCleaned, err := t.cleanupMigrationResources(ctx, t.Owner.Status.CompletedMigrations); err != nil {
			return err
		} else if !allCleaned {
			t.Requeue = PollReQ
		} else {
			t.Owner.RemoveFinalizer(migrations.VirtualMachineStorageMigrationFinalizer, t.Log)
			t.Owner.Status.Phase = migrations.Completed
		}
	case migrations.Canceled:
		t.Owner.Status.DeleteCondition(string(migrations.Canceling))
		t.Owner.Status.SetCondition(migrations.Condition{
			Type:     string(migrations.Canceled),
			Status:   corev1.ConditionTrue,
			Reason:   Cancel,
			Category: migrations.Advisory,
			Message:  "The migration has been canceled.",
		})
		t.Owner.RemoveFinalizer(migrations.VirtualMachineStorageMigrationFinalizer, t.Log)
	default:
		t.Requeue = NoReQ
	}
	return nil
}

func (t *Task) isLiveMigrationCompleted(ctx context.Context, vmName string) (bool, error) {
	// In order to determine if the live migration is complete, we need to check the VMIM status.
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return false, err
	}
	var activeVMIM *virtv1.VirtualMachineInstanceMigration
	for _, vmim := range vmimList.Items {
		if vmim.Spec.VMIName == vmName && vmim.Status.Phase != virtv1.MigrationFailed {
			t.Log.V(5).Info("Found active VMIM", "vmim", vmim.Name)
			activeVMIM = &vmim
			break
		}
	}
	if activeVMIM == nil {
		return false, nil
	}
	t.Log.V(5).Info("is active VMIM completed", "completed", activeVMIM.Status.MigrationState != nil && activeVMIM.Status.MigrationState.Completed && !activeVMIM.Status.MigrationState.Failed)
	return activeVMIM.Status.MigrationState != nil && activeVMIM.Status.MigrationState.Completed && !activeVMIM.Status.MigrationState.Failed, nil
}

func (t *Task) cleanupMigrationResources(ctx context.Context, completedMigrationsVMNames []string) (allCleaned bool, err error) {
	if allCleaned, err := t.cleanupCompletedPods(ctx, completedMigrationsVMNames); err != nil {
		return false, err
	} else if !allCleaned {
		return false, nil
	}
	if err := t.cleanupCompletedVMIMs(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Task) cleanupCompletedVMIMs(ctx context.Context) error {
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return err
	}

	completedMigrations := make(map[string]struct{})
	for _, migration := range t.Owner.Status.CompletedMigrations {
		completedMigrations[migration] = struct{}{}
	}
	for _, vmim := range vmimList.Items {
		t.Log.Info("Checking if VMIM is completed", "vmim", vmim.Name, "phase", vmim.Status.Phase)
		_, ok := completedMigrations[vmim.Spec.VMIName]
		if vmim.Status.Phase == virtv1.MigrationSucceeded && ok {
			t.Log.Info("Cleaning up migration resource", "vmim", vmim.Name)
			if err := t.Client.Delete(ctx, &vmim); err != nil {
				if !k8serrors.IsNotFound(err) {
					return err
				}
			}
		}
	}
	return nil
}

func (t *Task) cleanupCompletedPods(ctx context.Context, completedMigrationsVMNames []string) (allCleaned bool, err error) {
	podList := &corev1.PodList{}
	labelSelector := map[string]string{virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue}
	if err := t.Client.List(ctx, podList, k8sclient.InNamespace(t.Owner.Namespace), k8sclient.MatchingLabels(labelSelector)); err != nil {
		return false, err
	}
	for _, pod := range podList.Items {
		t.Log.Info("Checking if pod is completed", "pod", pod.Name, "phase", pod.Status.Phase)
		if pod.Status.Phase == corev1.PodSucceeded {
			t.Log.Info("Cleaning up migration resource", "pod", pod.Name)
			if err := t.Client.Delete(ctx, &pod); err != nil {
				if !k8serrors.IsNotFound(err) {
					return false, err
				}
			}
		}
	}
	for _, completedMigrationVMName := range completedMigrationsVMNames {
		vmi := &virtv1.VirtualMachineInstance{}
		if err := t.Client.Get(ctx, k8sclient.ObjectKey{Namespace: t.Owner.Namespace, Name: completedMigrationVMName}, vmi); err != nil {
			if !k8serrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		if len(vmi.Status.ActivePods) > 1 {
			// Not all pods are cleaned up, so we need to requeue.
			return false, nil
		}
	}
	return true, nil
}

// Initialize.
func (t *Task) init() {
	t.Log.V(4).Info("Running task init")
	if t.Owner.Status.Phase == "" {
		t.Owner.Status.Phase = migrations.Started
	}
}
