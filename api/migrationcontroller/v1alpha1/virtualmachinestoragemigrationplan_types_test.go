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
