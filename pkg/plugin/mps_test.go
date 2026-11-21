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
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

func TestGetMPSOptions(t *testing.T) {
	mockRM := &rm.ResourceManagerMock{
		ResourceFunc: func() spec.ResourceName {
			return spec.ResourceName("nvidia.com/gpu")
		},
		DevicesFunc: func() rm.Devices {
			return rm.Devices{
				"GPU-0": &rm.Device{
					Device: pluginapi.Device{ID: "GPU-0"},
				},
			}
		},
	}
	mpsRoot := "/tmp/mps"
	opts := &options{
		config: &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					MpsRoot: &mpsRoot,
				},
			},
			Sharing: spec.Sharing{
				MPS: &spec.ReplicatedResources{
					Resources: []spec.ReplicatedResource{
						{
							Name:     spec.ResourceName("nvidia.com/gpu"),
							Replicas: 2,
						},
					},
				},
			},
		},
	}
	mpsOpts, err := opts.getMPSOptions(mockRM)
	require.NoError(t, err)
	require.True(t, mpsOpts.enabled)
	require.Equal(t, defaultMPSReadyFilePath, mpsOpts.readyFilePath)
	require.Equal(t, defaultMPSReadyTimeout, mpsOpts.readyTimeout)
	require.Equal(t, defaultMPSRetryInterval, mpsOpts.retryInterval)
}

func TestWaitForDaemon_DisabledOrNil(t *testing.T) {
	var nilOpts *mpsOptions
	require.NoError(t, nilOpts.waitForDaemon(context.Background()))

	disabledOpts := &mpsOptions{enabled: false}
	require.NoError(t, disabledOpts.waitForDaemon(context.Background()))
}

func TestWaitForDaemon_ImmediateSuccess(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  500 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(context.Context) error {
			return nil
		},
	}

	require.NoError(t, opts.waitForDaemon(context.Background()))
}

func TestWaitForDaemon_FailureWhenReadyFileAbsent(t *testing.T) {
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: filepath.Join(t.TempDir(), ".ready"),
		readyTimeout:  50 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(context.Context) error {
			return nil
		},
	}

	err := opts.waitForDaemon(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout waiting for MPS daemon health")
	require.Contains(t, err.Error(), "ready file not present")
}

func TestWaitForDaemon_RetryUntilHealthy(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	var healthAttempts int32
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  1 * time.Second,
		retryInterval: 20 * time.Millisecond,
		healthChecker: func(context.Context) error {
			cur := atomic.AddInt32(&healthAttempts, 1)
			if cur < 2 {
				return errors.New("daemon still starting")
			}
			return nil
		},
	}

	err := opts.waitForDaemon(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, atomic.LoadInt32(&healthAttempts), int32(2))
}

func TestWaitForDaemon_PolledReadyFileOnDisk(t *testing.T) {
	tmpDir := t.TempDir()
	readyFilePath := filepath.Join(tmpDir, ".ready")

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFilePath,
		readyTimeout:  1 * time.Second,
		retryInterval: 20 * time.Millisecond,
		healthChecker: func(context.Context) error {
			return nil
		},
	}

	go func() {
		time.Sleep(60 * time.Millisecond)
		if err := os.MkdirAll(filepath.Dir(readyFilePath), 0755); err != nil {
			t.Errorf("failed to create directory: %v", err)
			return
		}
		f, err := os.Create(readyFilePath)
		if err != nil {
			t.Errorf("failed to create ready file: %v", err)
			return
		}
		_ = f.Close()
	}()

	err := opts.waitForDaemon(context.Background())
	require.NoError(t, err)
}

func TestWaitForDaemon_ReadyFilePathPresence(t *testing.T) {
	tmpDir := t.TempDir()
	readyFile := filepath.Join(tmpDir, ".ready")
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
	}

	present, err := opts.isReadyFilePresent()
	require.NoError(t, err)
	require.False(t, present)

	f, err := os.Create(readyFile)
	require.NoError(t, err)
	_ = f.Close()

	present, err = opts.isReadyFilePresent()
	require.NoError(t, err)
	require.True(t, present)
}

func TestWaitForDaemon_ReadyFileStatError(t *testing.T) {
	tmpDir := t.TempDir()
	regFile := filepath.Join(tmpDir, "regular-file")
	require.NoError(t, os.WriteFile(regFile, []byte("data"), 0644))
	invalidPath := filepath.Join(regFile, ".ready")

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: invalidPath,
		readyTimeout:  50 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(context.Context) error {
			return nil
		},
	}

	present, err := opts.isReadyFilePresent()
	require.Error(t, err)
	require.False(t, present)

	waitErr := opts.waitForDaemon(context.Background())
	require.Error(t, waitErr)
	require.Contains(t, waitErr.Error(), "timeout waiting for MPS daemon health")
	require.NotContains(t, waitErr.Error(), "ready file not present")
}

func TestWaitForDaemon_TimeoutFailure(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  60 * time.Millisecond,
		retryInterval: 15 * time.Millisecond,
		healthChecker: func(context.Context) error {
			return errors.New("daemon unavailable")
		},
	}

	err := opts.waitForDaemon(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout waiting for MPS daemon health")
	require.Contains(t, err.Error(), "daemon unavailable")
}

func TestWaitForDaemon_ContextExpirationDoesNotMaskRealError(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	firstCall := true
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  50 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(ctx context.Context) error {
			if firstCall {
				firstCall = false
				return errors.New("daemon connection refused")
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}

	err := opts.waitForDaemon(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "daemon connection refused")
	require.NotContains(t, err.Error(), "(last error: context deadline exceeded)")
}

func TestWaitForDaemon_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	firstAttemptFailed := make(chan struct{})
	var once sync.Once

	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  5 * time.Second,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(context.Context) error {
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

	err := opts.waitForDaemon(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled")
}

func TestWaitForDaemon_HealthContextHasDeadline(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), ".ready")
	require.NoError(t, os.WriteFile(readyFile, nil, 0644))

	var capturedCtx context.Context
	opts := &mpsOptions{
		enabled:       true,
		resourceName:  spec.ResourceName("nvidia.com/gpu"),
		readyFilePath: readyFile,
		readyTimeout:  500 * time.Millisecond,
		retryInterval: 10 * time.Millisecond,
		healthChecker: func(ctx context.Context) error {
			capturedCtx = ctx
			return nil
		},
	}

	require.NoError(t, opts.waitForDaemon(context.Background()))
	require.NotNil(t, capturedCtx)
	_, hasDeadline := capturedCtx.Deadline()
	require.True(t, hasDeadline, "health check context should have a deadline")
}
