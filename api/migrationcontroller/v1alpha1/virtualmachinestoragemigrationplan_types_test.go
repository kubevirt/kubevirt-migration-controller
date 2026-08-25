// Package v1alpha1 tests use the stdlib testing package to avoid pulling
// Ginkgo into this module.
package v1alpha1

import "testing"

func TestAllSpecVMsCompleted(t *testing.T) {
	spec := []VirtualMachineStorageMigrationPlanVirtualMachine{
		{Name: "vm-a"},
		{Name: "vm-b"},
	}

	t.Run("false when spec is empty", func(t *testing.T) {
		if AllSpecVMsCompleted(nil, nil) {
			t.Fatal("expected false for empty spec")
		}
	})

	t.Run("false when completed count mismatches", func(t *testing.T) {
		completed := []VirtualMachineStorageMigrationPlanStatusVirtualMachine{{
			VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"},
		}}
		if AllSpecVMsCompleted(spec, completed) {
			t.Fatal("expected false when completed count mismatches")
		}
	})

	t.Run("false when completed VM names do not match spec", func(t *testing.T) {
		completed := []VirtualMachineStorageMigrationPlanStatusVirtualMachine{
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"}},
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-wrong"}},
		}
		if AllSpecVMsCompleted(spec, completed) {
			t.Fatal("expected false when completed VM names do not match spec")
		}
	})

	t.Run("false when completed VM names are duplicated", func(t *testing.T) {
		completed := []VirtualMachineStorageMigrationPlanStatusVirtualMachine{
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"}},
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"}},
		}
		if AllSpecVMsCompleted(spec, completed) {
			t.Fatal("expected false when completed VM names are duplicated")
		}
	})

	t.Run("true when every spec VM is completed", func(t *testing.T) {
		completed := []VirtualMachineStorageMigrationPlanStatusVirtualMachine{
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-b"}},
			{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"}},
		}
		if !AllSpecVMsCompleted(spec, completed) {
			t.Fatal("expected true when every spec VM is completed")
		}
	})
}

func TestPlanStatusShowsCompleted(t *testing.T) {
	spec := []VirtualMachineStorageMigrationPlanVirtualMachine{{Name: "vm-a"}}
	completed := []VirtualMachineStorageMigrationPlanStatusVirtualMachine{
		{VirtualMachineStorageMigrationPlanVirtualMachine: VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"}},
	}

	t.Run("false when status is nil", func(t *testing.T) {
		if PlanStatusShowsCompleted(spec, nil) {
			t.Fatal("expected false for nil status")
		}
	})

	t.Run("false when in-progress migrations remain", func(t *testing.T) {
		status := &VirtualMachineStorageMigrationPlanStatus{
			CompletedMigrations:  completed,
			InProgressMigrations: completed,
		}
		if PlanStatusShowsCompleted(spec, status) {
			t.Fatal("expected false when in-progress migrations remain")
		}
	})

	t.Run("false when ready migrations remain", func(t *testing.T) {
		status := &VirtualMachineStorageMigrationPlanStatus{
			CompletedMigrations: completed,
			ReadyMigrations:     completed,
		}
		if PlanStatusShowsCompleted(spec, status) {
			t.Fatal("expected false when ready migrations remain")
		}
	})

	t.Run("true when every spec VM is completed and no work remains", func(t *testing.T) {
		status := &VirtualMachineStorageMigrationPlanStatus{
			CompletedMigrations: completed,
		}
		if !PlanStatusShowsCompleted(spec, status) {
			t.Fatal("expected true when plan status shows completion")
		}
	})
}
