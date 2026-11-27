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

package mpsv3

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

func TestAllocatorReady(t *testing.T) {
	mgr := NewManager()
	allocator := NewAllocator(mgr)

	if allocator.Mode() != OperatingModeMPSV3 {
		t.Fatalf("expected Mode() = %q, got %q", OperatingModeMPSV3, allocator.Mode())
	}

	// Driver branch R620 valid
	if !allocator.Ready(HostCapabilities{DriverVersion: "620.70.06"}) {
		t.Errorf("expected Ready() = true for driver 620.70.06")
	}

	// Older driver invalid
	if allocator.Ready(HostCapabilities{DriverVersion: "550.54.14"}) {
		t.Errorf("expected Ready() = false for driver 550.54.14")
	}

	// Empty driver invalid
	if allocator.Ready(HostCapabilities{}) {
		t.Errorf("expected Ready() = false for empty driver")
	}
}

func TestAllocatorAllocate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	root := filepath.Join(tmpDir, "mps-v3")
	cdiRoot := filepath.Join(tmpDir, "cdi")

	mock := newMockDaemonRunner()
	mgr := NewManager(
		WithRoot(root),
		WithCDIRoot(cdiRoot),
		WithCommandRunner(mock.Run),
	)
	ctx := context.Background()
	_ = mgr.Start(ctx, map[string]bool{})
	defer mgr.Close()

	allocator := NewAllocator(mgr)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "12345-67890",
			Annotations: map[string]string{
				MPSV3ComputeModeAnnotation: ComputeModeNamespaceCap,
			},
		},
	}
	ctr := v1.Container{
		Name: "gpu-worker",
	}
	devs := util.ContainerDevices{
		{
			UUID:      "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Usedmem:   4096,
			Usedcores: 50,
		},
	}

	response := &pluginapi.ContainerAllocateResponse{
		Envs: make(map[string]string),
	}

	err = allocator.Allocate(ctx, pod, ctr, devs, response)
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}

	// Verify environment variables injected
	if response.Envs["CUDA_MPS_PIPE_DIRECTORY"] != ContainerPipePath {
		t.Errorf("expected pipe dir %s, got %s", ContainerPipePath, response.Envs["CUDA_MPS_PIPE_DIRECTORY"])
	}
	if response.Envs["NVIDIA_GPU_MEMORY_REQUEST"] != "4096" {
		t.Errorf("expected memory request 4096, got %s", response.Envs["NVIDIA_GPU_MEMORY_REQUEST"])
	}

	// Verify CDI devices injected
	if len(response.CdiDevices) != 1 {
		t.Fatalf("expected 1 CDI device, got %d", len(response.CdiDevices))
	}
	expectedCDIPrefix := CDIKind + "=ns_"
	if len(response.CdiDevices[0].Name) < len(expectedCDIPrefix) || response.CdiDevices[0].Name[:len(expectedCDIPrefix)] != expectedCDIPrefix {
		t.Errorf("unexpected CDI device name: %s", response.CdiDevices[0].Name)
	}
}

func TestAllocatorRejectsReservedContainerEnv(t *testing.T) {
	mgr := NewManager()
	allocator := NewAllocator(mgr)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			UID:  "123",
		},
	}
	ctr := v1.Container{
		Name: "bad-container",
		Env: []v1.EnvVar{
			{Name: "NVIDIA_GPU_MEMORY_LIMIT", Value: "1000"},
		},
	}
	devs := util.ContainerDevices{
		{UUID: "GPU-1", Usedmem: 100, Usedcores: 10},
	}
	response := &pluginapi.ContainerAllocateResponse{}

	err := allocator.Allocate(context.Background(), pod, ctr, devs, response)
	if err == nil {
		t.Fatalf("expected Allocate to fail when container sets reserved MPS env var")
	}
}
