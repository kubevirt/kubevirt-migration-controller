package reference

import (
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func RefSet(ref *corev1.ObjectReference) bool {
	return ref != nil &&
		ref.Namespace != "" &&
		ref.Name != ""
}

func RefEquals(refA, refB *corev1.ObjectReference) bool {
	if refA == nil || refB == nil {
		return false
	}
	return reflect.DeepEqual(refA, refB)
}

func ToKind(resource interface{}) string {
	t := reflect.TypeOf(resource).String()
	p := strings.SplitN(t, ".", 2)
	return p[len(p)-1]
}
