/**
# Copyright 2024 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/mps"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

func TestWaitForDaemon_DisabledOrNil(t *testing.T) {
	var nilOpts *mpsOptions
	require.NoError(t, nilOpts.waitForDaemon())

	disabledOpts := &mpsOptions{enabled: false}
	require.NoError(t, disabledOpts.waitForDaemon())
}

func TestWaitForDaemon_ImmediateSuccess(t *testing.T) {
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  500 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func() error {
			return nil
		},
		readyFileChecker: func() bool {
			return true
		},
	}

	require.NoError(t, opts.waitForDaemon())
}

func TestWaitForDaemon_FailureWhenReadyFileAbsent(t *testing.T) {
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  50 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func() error {
			return nil
		},
		readyFileChecker: func() bool {
			return false
		},
	}

	err := opts.waitForDaemon()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout waiting for MPS daemon health")
	require.Contains(t, err.Error(), "ready file not present")
}

func TestWaitForDaemon_RetryUntilHealthy(t *testing.T) {
	var readyChecks int32
	var healthAttempts int32
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  1 * time.Second,
		retryInterval: 20 * time.Millisecond,
		healthChecker: func() error {
			cur := atomic.AddInt32(&healthAttempts, 1)
			if cur < 2 {
				return errors.New("daemon still starting")
			}
			return nil
		},
		readyFileChecker: func() bool {
			cur := atomic.AddInt32(&readyChecks, 1)
			return cur >= 2
		},
	}

	err := opts.waitForDaemon()
	require.NoError(t, err)
	require.GreaterOrEqual(t, atomic.LoadInt32(&readyChecks), int32(2))
	require.GreaterOrEqual(t, atomic.LoadInt32(&healthAttempts), int32(2))
}

func TestWaitForDaemon_PolledReadyFileOnDisk(t *testing.T) {
	tmpDir := t.TempDir()
	mockRM := &rm.ResourceManagerMock{
		ResourceFunc: func() spec.ResourceName {
			return spec.ResourceName("nvidia.com/gpu")
		},
	}
	daemon := mps.NewDaemon(mockRM, mps.Root(tmpDir))
	readyFilePath := daemon.ReadyFile()

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		daemon:        daemon,
		readyTimeout:  1 * time.Second,
		retryInterval: 20 * time.Millisecond,
		healthChecker: func() error {
			return nil
		},
	}

	go func() {
		time.Sleep(60 * time.Millisecond)
		require.NoError(t, os.MkdirAll(filepath.Dir(readyFilePath), 0755))
		f, err := os.Create(readyFilePath)
		if err == nil {
			_ = f.Close()
		}
	}()

	err := opts.waitForDaemon()
	require.NoError(t, err)
}

func TestWaitForDaemon_ProductionReadyPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mockRM := &rm.ResourceManagerMock{
		ResourceFunc: func() spec.ResourceName {
			return spec.ResourceName("nvidia.com/gpu")
		},
	}
	daemon := mps.NewDaemon(mockRM, mps.Root(tmpDir))

	opts := &mpsOptions{
		enabled:      true,
		resourceName: spec.ResourceName("nvidia.com/gpu"),
		daemon:       daemon,
	}

	require.False(t, opts.isReadyFilePresent())

	// Test ReadyFile path
	readyFilePath := daemon.ReadyFile()
	require.NoError(t, os.MkdirAll(filepath.Dir(readyFilePath), 0755))
	f, err := os.Create(readyFilePath)
	require.NoError(t, err)
	_ = f.Close()
	require.True(t, opts.isReadyFilePresent())
	_ = os.Remove(readyFilePath)

	// Test PipeDir .ready path
	pipeReadyPath := filepath.Join(daemon.PipeDir(), ".ready")
	require.NoError(t, os.MkdirAll(filepath.Dir(pipeReadyPath), 0755))
	f, err = os.Create(pipeReadyPath)
	require.NoError(t, err)
	_ = f.Close()
	require.True(t, opts.isReadyFilePresent())
}

func TestWaitForDaemon_BlockedHealthCheck(t *testing.T) {
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  50 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		readyFileChecker: func() bool {
			return true
		},
		healthChecker: func() error {
			time.Sleep(500 * time.Millisecond)
			return nil
		},
	}

	err := opts.waitForDaemon()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout waiting for MPS daemon health")
}

func TestWaitForDaemon_TimeoutFailure(t *testing.T) {
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  60 * time.Millisecond,
		retryInterval: 15 * time.Millisecond,
		readyFileChecker: func() bool {
			return true
		},
		healthChecker: func() error {
			return errors.New("daemon unavailable")
		},
	}

	err := opts.waitForDaemon()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout waiting for MPS daemon health")
	require.Contains(t, err.Error(), "daemon unavailable")
}

func TestWaitForDaemon_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstAttemptFailed := make(chan struct{})
	var once sync.Once

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyTimeout:  5 * time.Second,
		retryInterval: 10 * time.Millisecond,
		readyFileChecker: func() bool {
			return true
		},
		healthChecker: func() error {
			once.Do(func() {
				close(firstAttemptFailed)
			})
			return errors.New("daemon still initializing")
		},
	}

	go func() {
		<-firstAttemptFailed
		cancel()
	}()

	err := opts.waitForDaemonWithContext(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled")
}

func TestUpdateResponse(t *testing.T) {
	nilOpts := (*mpsOptions)(nil)
	resp := &pluginapi.ContainerAllocateResponse{
		Envs: make(map[string]string),
	}
	nilOpts.updateReponse(resp)
	require.Empty(t, resp.Envs)

	disabledOpts := &mpsOptions{enabled: false}
	disabledOpts.updateReponse(resp)
	require.Empty(t, resp.Envs)

	mockRM := &rm.ResourceManagerMock{
		ResourceFunc: func() spec.ResourceName {
			return spec.ResourceName("nvidia.com/gpu")
		},
	}
	daemon := mps.NewDaemon(mockRM, mps.ContainerRoot)
	root := mps.Root("/test/mps-root")
	opts := &mpsOptions{
		enabled:      true,
		resourceName: spec.ResourceName("nvidia.com/gpu"),
		daemon:       daemon,
		hostRoot:     root,
	}

	opts.updateReponse(resp)
	require.Equal(t, "/mps/nvidia.com/gpu/pipe", resp.Envs["CUDA_MPS_PIPE_DIRECTORY"])
	require.Len(t, resp.Mounts, 2)
	require.Equal(t, "/mps/nvidia.com/gpu/pipe", resp.Mounts[0].ContainerPath)
	require.Equal(t, filepath.Join("/test/mps-root", "nvidia.com/gpu", "pipe"), resp.Mounts[0].HostPath)
	require.Equal(t, "/dev/shm", resp.Mounts[1].ContainerPath)
	require.Equal(t, filepath.Join("/test/mps-root", "shm"), resp.Mounts[1].HostPath)
}
