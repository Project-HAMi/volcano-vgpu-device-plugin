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

package imex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
)

var osStat = os.Stat

// Channels represents a set of IMEX channels.
type Channels []*Channel

// Channel represents an IMEX channel.
type Channel struct {
	ID       string
	Path     string
	HostPath string
}

// GetChannels returns the set of channels for the given config.
// devRoot represents the device root in the current container for device discovery,
// while config.Flags.NvidiaDevRoot defines the host root for device node mounting.
// If the selection of the default IMEX channel is disabled no channels are returned.
func GetChannels(config *spec.Config, devRoot string) (Channels, error) {
	hostDevRoot := getHostDevRoot(config)

	var channels Channels
	for _, channelID := range config.Imex.ChannelIDs {
		id := fmt.Sprintf("%d", channelID)
		channelName := "channel" + id
		path := filepath.Join("/dev/nvidia-caps-imex-channels", channelName)
		channel := Channel{
			ID:       id,
			Path:     path,
			HostPath: filepath.Join(hostDevRoot, path),
		}
		discoveryPath := filepath.Join(devRoot, path)

		// Check whether the channel exists at candidate locations (host path, discovery path, default container path).
		exists, err := channelExists(channel.HostPath, discoveryPath, path)
		// If discovered via a container-side candidate but hostDevRoot is explicitly configured,
		// verify that the corresponding host path exists so that non-CDI PassDeviceSpecs does not
		// return an absent host path to kubelet.
		if exists && channel.HostPath != "" && channel.HostPath != discoveryPath && channel.HostPath != path {
			if hostExists, hostErr := channelExists(channel.HostPath); !hostExists {
				exists = false
				err = errors.Join(err, hostErr, fmt.Errorf("host path %v does not exist", channel.HostPath))
			}
		}

		if !exists {
			if config.Imex.Required {
				return nil, errors.Join(err, fmt.Errorf("requested IMEX channel %v does not exist", channelName))
			}
			klog.Warningf("Ignoring requested IMEX channel %v (%v)", channelName, err)
			continue
		}
		klog.Infof("Selecting IMEX channel %v", channelName)
		channels = append(channels, &channel)
	}
	return channels, nil
}

// getHostDevRoot resolves the host device root from configuration flags.
// It prefers NvidiaDevRoot, falling back to NvidiaDriverRoot.
func getHostDevRoot(config *spec.Config) string {
	if config == nil {
		return ""
	}
	if config.Flags.NvidiaDevRoot != nil && *config.Flags.NvidiaDevRoot != "" {
		return *config.Flags.NvidiaDevRoot
	}
	if config.Flags.NvidiaDriverRoot != nil && *config.Flags.NvidiaDriverRoot != "" {
		return *config.Flags.NvidiaDriverRoot
	}
	return ""
}

// exists checks whether the IMEX channel exists on the host or in the container.
func (c Channel) exists() (bool, error) {
	paths := []string{c.HostPath}
	if c.HostPath != c.Path {
		paths = append(paths, c.Path)
	}
	return channelExists(paths...)
}

// channelExists checks whether any of the candidate paths exists and represents a character device.
func channelExists(paths ...string) (bool, error) {
	var errs error
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := osStat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		if info.Mode()&os.ModeCharDevice == 0 {
			errs = errors.Join(errs, fmt.Errorf("%v is not a character device", path))
			continue
		}
		return true, nil
	}
	return false, errs
}
