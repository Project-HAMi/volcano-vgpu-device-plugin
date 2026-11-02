/*
Copyright 2026 The HAMi Authors.

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

package util

import (
	"errors"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestPodAllocationComplete(t *testing.T) {
	unavailable := errors.New("API unavailable")
	for _, tt := range []struct {
		name     string
		pending  string
		getErr   error
		complete bool
	}{
		{name: "failed read cannot confirm completion", getErr: unavailable},
		{name: "pending devices", pending: "GPU-a,NVIDIA,512,50:"},
		{name: "all devices allocated", complete: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gpu-pod", Namespace: "default"}}
			refreshed := pod.DeepCopy()
			refreshed.Annotations = map[string]string{AssignedIDsToAllocateAnnotations: tt.pending}
			cs := fake.NewClientset(refreshed)
			if tt.getErr != nil {
				cs.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.getErr
				})
			}
			complete, err := podAllocationComplete(cs, pod)
			if complete != tt.complete || !errors.Is(err, tt.getErr) {
				t.Fatalf("completion = %v, error = %v; want %v, %v", complete, err, tt.complete, tt.getErr)
			}
		})
	}
}
