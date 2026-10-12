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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

// This cross-component fixture requires the allocation, cache and MIG PRs.
func newIntegratedVolcanoTestPlugin(t *testing.T, pod *corev1.Pod) (*nvidiaDevicePlugin, *testPodOps) {
	t.Helper()
	plugin, ops := newVolcanoTestPlugin(t, pod)
	if plugin.cacheManager != nil {
		return plugin, ops
	}
	list := func() ([]*corev1.Pod, error) { return []*corev1.Pod{pod}, nil }
	manager, err := vgpucache.New(vgpucache.Config{Root: filepath.Join(os.Getenv("HOOK_PATH"), "vgpu", "containers"), ScanInterval: time.Second}, list, list)
	require.NoError(t, err)
	plugin.cacheManager = manager
	return plugin, ops
}

func TestIntegratedAllocateCacheFailureDoesNotConsumeAnnotation(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "active", Annotations: map[string]string{util.AssignedIDsToAllocateAnnotations: "GPU-A,NVIDIA,1000,20:"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}}}
	plugin, ops := newIntegratedVolcanoTestPlugin(t, pod)
	root := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(root, nil, 0600))
	list := func() ([]*corev1.Pod, error) { return []*corev1.Pod{pod}, nil }
	manager, err := vgpucache.New(vgpucache.Config{Root: root, ScanInterval: time.Second}, list, list)
	require.NoError(t, err)
	plugin.cacheManager = manager
	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"replica-0"}}}})
	require.Nil(t, response)
	require.ErrorContains(t, err, "prepare vGPU cache")
	require.Nil(t, ops.patched)
	require.Zero(t, ops.succeeded)
}

func TestIntegratedAllocateRetryPreservesCacheFile(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "active", Annotations: map[string]string{util.AssignedIDsToAllocateAnnotations: "GPU-A,NVIDIA,1000,20:"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}}}
	plugin, _ := newIntegratedVolcanoTestPlugin(t, pod)
	request := &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"replica-0"}}}}
	first, err := plugin.Allocate(context.Background(), request)
	require.NoError(t, err)
	cacheName := first.ContainerResponses[0].Envs["CUDA_DEVICE_MEMORY_SHARED_CACHE"]
	path := filepath.Join(os.Getenv("HOOK_PATH"), "vgpu", "containers", "active_gpu", filepath.Base(cacheName))
	require.NoError(t, os.WriteFile(path, []byte("active usage"), 0600))
	again, err := plugin.Allocate(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, cacheName, again.ContainerResponses[0].Envs["CUDA_DEVICE_MEMORY_SHARED_CACHE"])
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "active usage", string(data))
}

func TestIntegratedAllocateMIGFailureDoesNotConsumeAnnotation(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "mig", Annotations: map[string]string{util.AssignedIDsToAllocateAnnotations: "GPU-A[group2-0],NVIDIA,1000,20:"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gpu"}}}}
	plugin, ops := newIntegratedVolcanoTestPlugin(t, pod)
	config.Mode = "mig"
	plugin.migBackend = &fakeMIG{exportErr: errors.New("hardware export unavailable")}
	response, err := plugin.Allocate(context.Background(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"replica-0"}}}})
	require.Nil(t, response)
	require.ErrorContains(t, err, "hardware export unavailable")
	require.Nil(t, ops.patched)
	require.Zero(t, ops.succeeded)
}
