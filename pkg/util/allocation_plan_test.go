/*
Copyright 2026 The Volcano Authors.

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
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPlanDeviceRequestsPreservesContainerOrderAndOtherDevices(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "workload", Annotations: map[string]string{
			AssignedIDsToAllocateAnnotations: ";GPU-B,NVIDIA,2000,30:Other,ASCEND,1,100:;GPU-A,NVIDIA,1000,20:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "cpu"}, {Name: "second"}, {Name: "third"}}},
	}
	requests, remaining, err := PlanDeviceRequests(NvidiaGPUDevice, pod, []int{1, 1})
	require.NoError(t, err)
	require.Len(t, requests, 2)
	require.Equal(t, "second", requests[0].Container.Name)
	require.Equal(t, "GPU-B", requests[0].Devices[0].UUID)
	require.EqualValues(t, 2000, requests[0].Devices[0].Usedmem)
	require.Equal(t, "third", requests[1].Container.Name)
	require.Equal(t, "GPU-A", requests[1].Devices[0].UUID)
	require.Equal(t, ";Other,ASCEND,1,100:;", remaining)
	// Planning must not consume the in-memory Pod before Allocate succeeds.
	require.Equal(t, ";GPU-B,NVIDIA,2000,30:Other,ASCEND,1,100:;GPU-A,NVIDIA,1000,20:", pod.Annotations[AssignedIDsToAllocateAnnotations])
}

func TestPlanDeviceRequestsRejectsMismatchWithoutConsumption(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			AssignedIDsToAllocateAnnotations: "GPU-A,NVIDIA,1000,20:;GPU-B,NVIDIA,2000,30:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "first"}, {Name: "second"}}},
	}
	_, _, err := PlanDeviceRequests(NvidiaGPUDevice, pod, []int{1, 2})
	require.ErrorContains(t, err, "device count mismatch")
	require.Equal(t, "GPU-A,NVIDIA,1000,20:;GPU-B,NVIDIA,2000,30:", pod.Annotations[AssignedIDsToAllocateAnnotations])

	pod.Annotations[AssignedIDsToAllocateAnnotations] = "GPU-A,NVIDIA,broken,20:"
	_, _, err = PlanDeviceRequests(NvidiaGPUDevice, pod, []int{1})
	require.ErrorContains(t, err, "invalid device allocation annotation")
}
