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
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/mps"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

const (
	defaultMPSReadyTimeout  = 30 * time.Second
	defaultMPSRetryInterval = 500 * time.Millisecond
)

var defaultMPSReadyFilePath = mps.ContainerRoot.ReadyFile()

type mpsOptions struct {
	enabled       bool
	resourceName  spec.ResourceName
	daemon        *mps.Daemon
	hostRoot      mps.Root
	readyTimeout  time.Duration
	retryInterval time.Duration
	readyFilePath string
	healthChecker func(context.Context) error
}

// getMPSOptions returns the MPS options specified for the resource manager.
// If MPS is not configured and empty set of options is returned.
func (o *options) getMPSOptions(resourceManager rm.ResourceManager) (mpsOptions, error) {
	if o.config.Sharing.SharingStrategy() != spec.SharingStrategyMPS {
		return mpsOptions{}, nil
	}

	for _, device := range resourceManager.Devices() {
		if device.IsMigDevice() {
			return mpsOptions{}, errors.New("sharing using MPS is not supported for MIG devices")
		}
	}

	m := mpsOptions{
		enabled:       true,
		resourceName:  resourceManager.Resource(),
		daemon:        mps.NewDaemon(resourceManager, mps.ContainerRoot),
		hostRoot:      mps.Root(*o.config.Flags.MpsRoot),
		readyTimeout:  defaultMPSReadyTimeout,
		retryInterval: defaultMPSRetryInterval,
		readyFilePath: defaultMPSReadyFilePath,
	}
	return m, nil
}

func (m *mpsOptions) isReadyFilePresent() (bool, error) {
	_, err := os.Stat(m.readyFilePath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (m *mpsOptions) assertHealthy(ctx context.Context) error {
	if m.healthChecker != nil {
		return m.healthChecker(ctx)
	}
	return m.daemon.AssertHealthy(ctx)
}

func (m *mpsOptions) waitForDaemon(parentCtx context.Context) error {
	if m == nil || !m.enabled {
		return nil
	}
	if err := parentCtx.Err(); err != nil {
		return fmt.Errorf("context canceled before waiting for MPS daemon: %w", err)
	}

	var lastErr error
	pollErr := wait.PollUntilContextTimeout(parentCtx, m.retryInterval, m.readyTimeout, true, func(ctx context.Context) (bool, error) {
		ready, err := m.isReadyFilePresent()
		if err != nil {
			if ctx.Err() == nil {
				lastErr = err
			}
			klog.V(4).InfoS("Unable to stat MPS ready file; retrying", "resource", m.resourceName, "path", m.readyFilePath, "err", err)
			return false, nil
		}
		if !ready {
			if ctx.Err() == nil {
				lastErr = errors.New("ready file not present")
			}
			klog.V(4).InfoS("Waiting for MPS daemon ready file", "resource", m.resourceName)
			return false, nil
		}

		if err := m.assertHealthy(ctx); err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				lastErr = err
			}
			klog.V(4).InfoS("MPS daemon ready file detected, but health check failed; retrying", "resource", m.resourceName, "err", err)
			return false, nil
		}

		klog.InfoS("MPS daemon is healthy and ready", "resource", m.resourceName)
		return true, nil
	})

	if pollErr != nil {
		if errors.Is(pollErr, context.Canceled) || errors.Is(parentCtx.Err(), context.Canceled) {
			return fmt.Errorf("context canceled while waiting for MPS daemon: %w", parentCtx.Err())
		}
		if lastErr != nil {
			return fmt.Errorf("timeout waiting for MPS daemon health: %w (last error: %v)", pollErr, lastErr)
		}
		return fmt.Errorf("timeout waiting for MPS daemon health: %w", pollErr)
	}
	return nil
}

func (m *mpsOptions) updateReponse(response *pluginapi.ContainerAllocateResponse) {
	if m == nil || !m.enabled {
		return
	}
	response.Envs["CUDA_MPS_PIPE_DIRECTORY"] = m.daemon.PipeDir()

	response.Mounts = append(response.Mounts,
		&pluginapi.Mount{
			ContainerPath: m.daemon.PipeDir(),
			HostPath:      m.hostRoot.PipeDir(m.resourceName),
		},
		&pluginapi.Mount{
			ContainerPath: m.daemon.ShmDir(),
			HostPath:      m.hostRoot.ShmDir(m.resourceName),
		},
	)
}
