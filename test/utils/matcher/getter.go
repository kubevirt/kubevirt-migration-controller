package matcher

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ThisPod fetches the latest state of the pod. If the object does not exist, nil is returned.
func ThisPod(pod *v1.Pod, client client.Client) func() (*v1.Pod, error) {
	return ThisPodWith(pod.Namespace, pod.Name, client)
}

// ThisPodWith fetches the latest state of the pod based on namespace and name.
// If the object does not exist, nil is returned.
func ThisPodWith(namespace string, name string, cli client.Client) func() (*v1.Pod, error) {
	return func() (*v1.Pod, error) {
		pod := &v1.Pod{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		// Since https://github.com/kubernetes/client-go/issues/861 we manually add the Kind
		pod.Kind = "Pod"
		return pod, nil
	}
}

// ThisVMI fetches the latest state of the VirtualMachineInstance. If the object does not exist, nil is returned.
func ThisVMI(vmi *virtv1.VirtualMachineInstance, client client.Client) func() (*virtv1.VirtualMachineInstance, error) {
	return ThisVMIWith(vmi.Namespace, vmi.Name, client)
}

// ThisVMIWith fetches the latest state of the VirtualMachineInstance based on namespace and name.
// If the object does not exist, nil is returned.
func ThisVMIWith(namespace string, name string, cli client.Client) func() (*virtv1.VirtualMachineInstance, error) {
	return func() (*virtv1.VirtualMachineInstance, error) {
		vmi := &virtv1.VirtualMachineInstance{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, vmi); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return vmi, nil
	}
}

// ThisVM fetches the latest state of the VirtualMachine. If the object does not exist, nil is returned.
func ThisVM(vm *virtv1.VirtualMachine, client client.Client) func() (*virtv1.VirtualMachine, error) {
	return ThisVMWith(vm.Namespace, vm.Name, client)
}

// ThisVMWith fetches the latest state of the VirtualMachine based on namespace and name.
// If the object does not exist, nil is returned.
func ThisVMWith(namespace string, name string, cli client.Client) func() (*virtv1.VirtualMachine, error) {
	return func() (*virtv1.VirtualMachine, error) {
		vm := &virtv1.VirtualMachine{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, vm); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return vm, nil
	}
}

// AllVMI fetches the latest state of all VMIs in a namespace.
func AllVMIs(namespace string, cli client.Client) func() ([]virtv1.VirtualMachineInstance, error) {
	return func() (p []virtv1.VirtualMachineInstance, err error) {
		vmis := &virtv1.VirtualMachineInstanceList{}
		if err := cli.List(context.Background(), vmis, &client.ListOptions{Namespace: namespace}); err != nil {
			return nil, err
		}
		return vmis.Items, nil
	}
}

// ThisDV fetches the latest state of the DataVolume. If the object does not exist, nil is returned.
func ThisDV(dv *v1beta1.DataVolume, client client.Client) func() (*v1beta1.DataVolume, error) {
	return ThisDVWith(dv.Namespace, dv.Name, client)
}

// ThisDVWith fetches the latest state of the DataVolume based on namespace and name.
// If the object does not exist, nil is returned.
func ThisDVWith(namespace string, name string, cli client.Client) func() (*v1beta1.DataVolume, error) {
	return func() (*v1beta1.DataVolume, error) {
		dv := &v1beta1.DataVolume{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, dv); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		// Since https://github.com/kubernetes/client-go/issues/861 we manually add the Kind
		dv.Kind = "DataVolume"
		return dv, nil
	}
}

// ThisPVC fetches the latest state of the PVC. If the object does not exist, nil is returned.
func ThisPVC(pvc *v1.PersistentVolumeClaim, client client.Client) func() (*v1.PersistentVolumeClaim, error) {
	return ThisPVCWith(pvc.Namespace, pvc.Name, client)
}

// ThisPVCWith fetches the latest state of the PersistentVolumeClaim based on namespace and name.
// If the object does not exist, nil is returned.
func ThisPVCWith(namespace string, name string, cli client.Client) func() (*v1.PersistentVolumeClaim, error) {
	return func() (*v1.PersistentVolumeClaim, error) {
		pvc := &v1.PersistentVolumeClaim{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, pvc); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		// Since https://github.com/kubernetes/client-go/issues/861 we manually add the Kind
		pvc.Kind = "PersistentVolumeClaim"
		return pvc, nil
	}
}

// ThisMigration fetches the latest state of the Migration. If the object does not exist, nil is returned.
func ThisMigration(
	migration *virtv1.VirtualMachineInstanceMigration,
	client client.Client) func() (*virtv1.VirtualMachineInstanceMigration, error) {
	return ThisMigrationWith(migration.Namespace, migration.Name, client)
}

// ThisMigrationWith fetches the latest state of the Migration based on namespace and name.
// If the object does not exist, nil is returned.
func ThisMigrationWith(
	namespace string,
	name string,
	cli client.Client) func() (*virtv1.VirtualMachineInstanceMigration, error) {
	return func() (*virtv1.VirtualMachineInstanceMigration, error) {
		migration := &virtv1.VirtualMachineInstanceMigration{}
		if err := cli.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, migration); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return migration, nil
	}
}
