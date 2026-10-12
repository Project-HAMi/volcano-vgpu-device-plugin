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

package plugin

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

type testPodOps struct {
	pod       *corev1.Pod
	count     int
	patched   map[string]string
	patchErr  error
	failed    int
	succeeded int
}

func (ops *testPodOps) PendingPod(_ string, count int) (*corev1.Pod, error) {
	ops.count = count
	return ops.pod, nil
}

func (ops *testPodOps) PatchAnnotations(_ *corev1.Pod, annotations map[string]string) error {
	ops.patched = annotations
	return ops.patchErr
}

func (ops *testPodOps) Failed(_ string, _ *corev1.Pod) { ops.failed++ }
func (ops *testPodOps) TrySuccess(_ string, _ *corev1.Pod) error {
	ops.succeeded++
	return nil
}

func newVolcanoTestPlugin(t *testing.T, pod *corev1.Pod) (*nvidiaDevicePlugin, *testPodOps) {
	t.Helper()
	t.Setenv("HOOK_PATH", t.TempDir())
	oldResource, oldMode, oldFactor := util.ResourceName, config.Mode, config.GPUMemoryFactor
	util.ResourceName = "volcano.sh/vgpu-number"
	config.Mode = "hami"
	config.GPUMemoryFactor = 1
	t.Cleanup(func() {
		util.ResourceName, config.Mode, config.GPUMemoryFactor = oldResource, oldMode, oldFactor
	})
	ops := &testPodOps{pod: pod}
	return &nvidiaDevicePlugin{
		rm: &rm.ResourceManagerMock{
			ResourceFunc: func() spec.ResourceName { return spec.ResourceName(util.ResourceName) },
			DevicesFunc: func() rm.Devices {
				return rm.Devices{
					"GPU-A::0": {Device: pluginapi.Device{ID: "GPU-A::0"}, Index: "0"},
					"GPU-B::0": {Device: pluginapi.Device{ID: "GPU-B::0"}, Index: "1"},
				}
			},
		},
		config: &spec.Config{Flags: spec.Flags{CommandLineFlags: spec.CommandLineFlags{
			Plugin: &spec.PluginCommandLineFlags{DeviceIDStrategy: ptr(spec.DeviceIDStrategyUUID)},
		}}},
		deviceListStrategies: spec.DeviceListStrategies{"envvar": true},
		podOps:               ops,
	}, ops
}

func TestAllocateVolcanoMultipleContainers(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "workload", UID: "pod-uid", Annotations: map[string]string{
			util.AssignedIDsToAllocateAnnotations: "GPU-B,NVIDIA,2000,30:;GPU-A,NVIDIA,1000,20:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "first"}, {Name: "second"}}},
	}
	plugin, ops := newVolcanoTestPlugin(t, pod)
	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{
		{DevicesIds: []string{"replica-0"}},
		{DevicesIds: []string{"replica-1"}},
	}})
	require.NoError(t, err)
	require.Equal(t, 2, ops.count)
	require.Len(t, response.ContainerResponses, 2)
	require.Equal(t, "GPU-B", response.ContainerResponses[0].Envs["NVIDIA_VISIBLE_DEVICES"])
	require.Equal(t, "2000m", response.ContainerResponses[0].Envs["CUDA_DEVICE_MEMORY_LIMIT_0"])
	require.Equal(t, "30", response.ContainerResponses[0].Envs["CUDA_DEVICE_SM_LIMIT"])
	require.Equal(t, "GPU-A", response.ContainerResponses[1].Envs["NVIDIA_VISIBLE_DEVICES"])
	require.Equal(t, "1000m", response.ContainerResponses[1].Envs["CUDA_DEVICE_MEMORY_LIMIT_0"])
	require.Equal(t, "20", response.ContainerResponses[1].Envs["CUDA_DEVICE_SM_LIMIT"])
	require.Equal(t, ";", ops.patched[util.AssignedIDsToAllocateAnnotations])
	require.Equal(t, 0, ops.failed)
	require.Equal(t, 1, ops.succeeded)
}

func TestAllocateVolcanoRejectsInvalidRequests(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.AssignedIDsToAllocateAnnotations: "GPU-A,NVIDIA,1000,20:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "first"}}},
	}
	plugin, ops := newVolcanoTestPlugin(t, pod)
	for _, request := range []*pluginapi.AllocateRequest{
		nil,
		{},
		{ContainerRequests: []*pluginapi.ContainerAllocateRequest{nil}},
		{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: nil}}},
	} {
		_, err := plugin.Allocate(context.Background(), request)
		require.Error(t, err)
	}
	require.Zero(t, ops.failed)
	require.Nil(t, ops.patched)

	_, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{
		{DevicesIds: []string{"replica-0", "replica-1"}},
	}})
	require.ErrorContains(t, err, "device count mismatch")
	require.Equal(t, 1, ops.failed)
	require.Nil(t, ops.patched)
}

func TestAllocateVolcanoAnnotationWriteFailure(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.AssignedIDsToAllocateAnnotations: "GPU-A,NVIDIA,1000,20:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "first"}}},
	}
	plugin, ops := newVolcanoTestPlugin(t, pod)
	ops.patchErr = errors.New("API update failed")
	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{
		{DevicesIds: []string{"replica-0"}},
	}})
	require.Nil(t, response)
	require.ErrorContains(t, err, "API update failed")
	require.Equal(t, 1, ops.failed)
	require.Zero(t, ops.succeeded)
	require.Equal(t, "GPU-A,NVIDIA,1000,20:", pod.Annotations[util.AssignedIDsToAllocateAnnotations])
}

func TestAllocateVolcanoRejectsUnregisteredGPU(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.AssignedIDsToAllocateAnnotations: "GPU-C,NVIDIA,1000,20:",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker"}}},
	}
	plugin, ops := newVolcanoTestPlugin(t, pod)
	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{
		{DevicesIds: []string{"replica-0"}},
	}})
	require.Nil(t, response)
	require.ErrorContains(t, err, `GPU-C" is not registered on this node`)
	require.Nil(t, ops.patched)
	require.Equal(t, "GPU-C,NVIDIA,1000,20:", pod.Annotations[util.AssignedIDsToAllocateAnnotations])
}

func TestAllocateVolcanoRejectsInvalidAnnotationWithoutConsumption(t *testing.T) {
	for _, annotation := range []string{
		",NVIDIA,1000,20:",
		"GPU-A,NVIDIA,-1,20:",
		"GPU-A,NVIDIA,1000,-1:",
	} {
		t.Run(annotation, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{util.AssignedIDsToAllocateAnnotations: annotation}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "worker"}}},
			}
			plugin, ops := newVolcanoTestPlugin(t, pod)
			response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIds: []string{"replica-0"}},
			}})
			require.Nil(t, response)
			require.ErrorContains(t, err, "invalid device allocation annotation")
			require.Nil(t, ops.patched)
			require.Equal(t, annotation, pod.Annotations[util.AssignedIDsToAllocateAnnotations])
		})
	}
}

type failingNVML struct{ nvml.Interface }

func (failingNVML) DeviceGetHandleByUUID(string) (nvml.Device, nvml.Return) {
	return nil, nvml.ERROR_NOT_FOUND
}

func TestConvertDeviceInfoNVMLFailure(t *testing.T) {
	oldFactor := config.GPUMemoryFactor
	config.GPUMemoryFactor = 1
	t.Cleanup(func() { config.GPUMemoryFactor = oldFactor })
	devices, err := convertDeviceInfo([]*pluginapi.Device{{ID: "GPU-A"}}, failingNVML{})
	require.Nil(t, devices)
	require.ErrorContains(t, err, "get NVML device")
}

type failingListWatchStream struct {
	pluginapi.DevicePlugin_ListAndWatchServer
	sends  int
	failAt int
}

func (stream *failingListWatchStream) Send(*pluginapi.ListAndWatchResponse) error {
	stream.sends++
	if stream.sends == stream.failAt {
		return errors.New("kubelet stream closed")
	}
	return nil
}

func TestListAndWatchPropagatesSendFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			plugin := &nvidiaDevicePlugin{
				rm: &rm.ResourceManagerMock{
					DevicesFunc:  func() rm.Devices { return nil },
					ResourceFunc: func() spec.ResourceName { return "nvidia.com/gpu" },
				},
				health: make(chan *rm.Device, 1),
				stop:   make(chan interface{}),
			}
			if failAt == 2 {
				plugin.health <- &rm.Device{Device: pluginapi.Device{ID: "GPU-A"}}
			}
			stream := &failingListWatchStream{failAt: failAt}
			err := plugin.ListAndWatch(&pluginapi.Empty{}, stream)
			require.ErrorContains(t, err, "kubelet stream closed")
			require.Equal(t, failAt, stream.sends)
		})
	}
}
