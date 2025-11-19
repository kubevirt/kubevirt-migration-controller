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
