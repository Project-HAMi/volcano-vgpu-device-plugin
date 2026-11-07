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
	"path/filepath"
	"time"

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

type mpsOptions struct {
	enabled          bool
	resourceName     spec.ResourceName
	daemon           *mps.Daemon
	hostRoot         mps.Root
	readyTimeout     time.Duration
	retryInterval    time.Duration
	healthChecker    func() error
	readyFileChecker func() bool
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
	}
	return m, nil
}

func (m *mpsOptions) isReadyFilePresent() bool {
	if m == nil {
		return false
	}
	if m.readyFileChecker != nil {
		return m.readyFileChecker()
	}
	if m.daemon == nil {
		return false
	}
	readyPaths := []string{
		filepath.Join(m.daemon.PipeDir(), ".ready"),
		filepath.Join(m.daemon.PipeDir(), "ready"),
		m.daemon.ReadyFile(),
	}
	for _, path := range readyPaths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func (m *mpsOptions) assertHealthy() error {
	if m.healthChecker != nil {
		return m.healthChecker()
	}
	if m.daemon == nil {
		return errors.New("MPS daemon is nil")
	}
	return m.daemon.AssertHealthy()
}

func (m *mpsOptions) waitForDaemon() error {
	return m.waitForDaemonWithContext(context.Background())
}

func (m *mpsOptions) waitForDaemonWithContext(parentCtx context.Context) error {
	if m == nil || !m.enabled {
		return nil
	}
	if err := parentCtx.Err(); err != nil {
		return fmt.Errorf("context canceled before waiting for MPS daemon: %w", err)
	}

	timeout := m.readyTimeout
	if timeout <= 0 {
		timeout = defaultMPSReadyTimeout
	}
	interval := m.retryInterval
	if interval <= 0 {
		interval = defaultMPSRetryInterval
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return fmt.Errorf("context canceled while waiting for MPS daemon: %w", err)
			}
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for MPS daemon health: %w (last error: %v)", err, lastErr)
			}
			return fmt.Errorf("timeout waiting for MPS daemon health: %w", err)
		}

		if m.isReadyFilePresent() {
			healthChan := make(chan error, 1)
			go func() {
				healthChan <- m.assertHealthy()
			}()

			select {
			case err := <-healthChan:
				if err == nil {
					if ctx.Err() == nil {
						klog.InfoS("MPS daemon is healthy and ready", "resource", m.resourceName)
						return nil
					}
				} else {
					lastErr = err
					klog.V(4).InfoS("MPS daemon ready file detected, but health check failed; retrying", "resource", m.resourceName, "err", err)
				}
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.Canceled) {
					return fmt.Errorf("context canceled while waiting for MPS daemon: %w", ctx.Err())
				}
				if lastErr != nil {
					return fmt.Errorf("timeout waiting for MPS daemon health: %w (last error: %v)", ctx.Err(), lastErr)
				}
				return fmt.Errorf("timeout waiting for MPS daemon health: %w", ctx.Err())
			}
		} else {
			lastErr = errors.New("ready file not present")
			klog.V(4).InfoS("Waiting for MPS daemon ready file", "resource", m.resourceName)
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("context canceled while waiting for MPS daemon: %w", ctx.Err())
			}
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for MPS daemon health: %w (last error: %v)", ctx.Err(), lastErr)
			}
			return fmt.Errorf("timeout waiting for MPS daemon health: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *mpsOptions) updateReponse(response *pluginapi.ContainerAllocateResponse) {
	if m == nil || !m.enabled || m.daemon == nil {
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
