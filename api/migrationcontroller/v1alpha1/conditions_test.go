package v1alpha1

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCopyConditionTimestampsFrom(t *testing.T) {
	origTime := metav1.NewTime(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	newTime := metav1.NewTime(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))

	orig := Conditions{List: []Condition{{
		Type:               Ready,
		Status:             corev1.ConditionTrue,
		Category:           Required,
		Message:            "ready",
		LastTransitionTime: origTime,
	}}}
	updated := Conditions{List: []Condition{{
		Type:               Ready,
		Status:             corev1.ConditionTrue,
		Category:           Required,
		Message:            "ready",
		LastTransitionTime: newTime,
	}}}

	compare := orig.DeepCopy()
	compare.CopyConditionTimestampsFrom(*updated.DeepCopy())
	if !compare.List[0].LastTransitionTime.Equal(&newTime) {
		t.Fatalf("expected updated timestamp %v, got %v", newTime, compare.List[0].LastTransitionTime)
	}
	if !conditionsEqual(*compare, *updated.DeepCopy()) {
		t.Fatal("expected conditions to be equal after copying timestamps")
	}
}

func conditionsEqual(a, b Conditions) bool {
	if len(a.List) != len(b.List) {
		return false
	}
	for i := range a.List {
		if !a.List[i].Equal(b.List[i]) {
			return false
		}
	}
	return true
}
