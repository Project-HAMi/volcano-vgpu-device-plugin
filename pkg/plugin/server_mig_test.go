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

package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/util/client"
)

// mockNvmlError mocks nvml.Interface returning ERROR_NOT_FOUND on device handle lookups.
type mockNvmlError struct {
	nvml.Interface
}

// DeviceGetHandleByUUID returns ERROR_NOT_FOUND for all device lookups.
func (mockNvmlError) DeviceGetHandleByUUID(string) (nvml.Device, nvml.Return) {
	return nil, nvml.ERROR_NOT_FOUND
}

// mockDeviceSuccess mocks nvml.Device returning valid model name and index.
type mockDeviceSuccess struct {
	nvml.Device
}

// GetName returns a dummy device model name and SUCCESS.
func (mockDeviceSuccess) GetName() (string, nvml.Return) {
	return "NVIDIA A100-SXM4-40GB", nvml.SUCCESS
}

// GetIndex returns a dummy device index and SUCCESS.
func (mockDeviceSuccess) GetIndex() (int, nvml.Return) {
	return 0, nvml.SUCCESS
}

// mockNvmlWithDevice mocks nvml.Interface returning a valid parent GPU but failing MIG handle lookup.
type mockNvmlWithDevice struct {
	nvml.Interface
}

// DeviceGetHandleByUUID returns a mock device handle and SUCCESS.
func (mockNvmlWithDevice) DeviceGetHandleByUUID(string) (nvml.Device, nvml.Return) {
	return mockDeviceSuccess{}, nvml.SUCCESS
}

// DeviceGetMigDeviceHandleByIndex returns ERROR_NOT_FOUND for MIG device handle lookup.
func (mockNvmlWithDevice) DeviceGetMigDeviceHandleByIndex(nvml.Device, int) (nvml.Device, nvml.Return) {
	return nil, nvml.ERROR_NOT_FOUND
}

// TestGetContainerDeviceStrArray_NonMIG verifies non-MIG devices are returned unmodified without NVML lookups.
func TestGetContainerDeviceStrArray_NonMIG(t *testing.T) {
	config.SetNvml(mockNvmlError{})
	t.Cleanup(config.ResetNvml)

	plugin := &nvidiaDevicePlugin{}
	devices := util.ContainerDevices{
		{UUID: "GPU-1234"},
		{UUID: "GPU-5678"},
	}

	result, err := plugin.GetContainerDeviceStrArray(devices)
	require.NoError(t, err)
	assert.Equal(t, []string{"GPU-1234", "GPU-5678"}, result)
}

// TestGetContainerDeviceStrArray_MIGLookupError verifies error propagation when GPU UUID lookup fails.
func TestGetContainerDeviceStrArray_MIGLookupError(t *testing.T) {
	config.SetNvml(mockNvmlError{})
	t.Cleanup(config.ResetNvml)

	plugin := &nvidiaDevicePlugin{}
	devices := util.ContainerDevices{
		{UUID: "GPU-invalid[0]"},
	}

	// Must return error, not panic
	result, err := plugin.GetContainerDeviceStrArray(devices)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestGetContainerDeviceStrArray_MIGTemplateError verifies error propagation when MIG template generation fails.
func TestGetContainerDeviceStrArray_MIGTemplateError(t *testing.T) {
	config.SetNvml(mockNvmlWithDevice{})
	t.Cleanup(config.ResetNvml)

	plugin := &nvidiaDevicePlugin{}
	devices := util.ContainerDevices{
		{UUID: "GPU-test[0]"},
	}

	result, err := plugin.GetContainerDeviceStrArray(devices)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to generate MIG template")
}

// TestAllocate_MIGLookupError_PodAllocationFailed verifies that when device resolution fails during Allocate
// for a MIG device request, Allocate calls PodAllocationFailed, releases the node lock, marks the pod as
// failed, and returns an error without panicking.
func TestAllocate_MIGLookupError_PodAllocationFailed(t *testing.T) {
	config.SetNvml(mockNvmlError{})
	t.Cleanup(config.ResetNvml)

	const testNode = "test-node"
	t.Setenv("NODE_NAME", testNode)

	origResource := util.ResourceName
	util.ResourceName = "volcano.sh/vgpu-number"
	t.Cleanup(func() { util.ResourceName = origResource })

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "test-pod-uid",
			Annotations: map[string]string{
				util.AssignedNodeAnnotations:          testNode,
				util.AssignedIDsToAllocateAnnotations: "GPU-invalid[0],NVIDIA,1000,10:",
				util.AssignedTimeAnnotations:          time.Now().Format(time.RFC3339),
				util.DeviceBindPhase:                  util.DeviceBindAllocating,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: testNode,
			Containers: []corev1.Container{
				{Name: "ctr-0"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	node := &corev1.Node{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"},
		ObjectMeta: metav1.ObjectMeta{
			Name: testNode,
			Annotations: map[string]string{
				util.VGPUDeviceName: time.Now().Format(time.RFC3339),
			},
		},
	}

	fakeClient := fake.NewClientset(node, pod)
	podResource := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	fakeClient.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		var patchData struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(patchAction.GetPatch(), &patchData); err != nil {
			return true, nil, err
		}
		obj, err := fakeClient.Tracker().Get(podResource, patchAction.GetNamespace(), patchAction.GetName())
		if err != nil {
			return true, nil, err
		}
		p := obj.(*corev1.Pod).DeepCopy()
		if p.Annotations == nil {
			p.Annotations = make(map[string]string)
		}
		for k, v := range patchData.Metadata.Annotations {
			p.Annotations[k] = v
		}
		if err := fakeClient.Tracker().Update(podResource, p, patchAction.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, p, nil
	})

	client.SetClient(fakeClient)
	t.Cleanup(client.ResetClient)

	plugin := &nvidiaDevicePlugin{
		rm: &rm.ResourceManagerMock{
			ResourceFunc: func() spec.ResourceName { return spec.ResourceName(util.ResourceName) },
		},
	}

	reqs := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{
				DevicesIds: []string{"test-gpu"},
			},
		},
	}

	resp, err := plugin.Allocate(context.Background(), reqs)
	require.Error(t, err, "Allocate should return error when MIG device lookup fails")
	assert.Contains(t, err.Error(), "failed to get index and type from UUID GPU-invalid[0]")
	assert.Equal(t, &pluginapi.AllocateResponse{}, resp)

	updatedPod, err := fakeClient.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, util.DeviceBindFailed, updatedPod.Annotations[util.DeviceBindPhase],
		"pod device bind phase should be marked failed")

	updatedNode, err := fakeClient.CoreV1().Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
	require.NoError(t, err)
	_, hasLock := updatedNode.Annotations[util.VGPUDeviceName]
	assert.False(t, hasLock, "node lock should be released")
}
