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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
)

type mockFileInfo struct {
	isCharDevice bool
}

func (m mockFileInfo) Name() string       { return "mock" }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) ModTime() time.Time { return time.Now() }
func (m mockFileInfo) IsDir() bool        { return false }
func (m mockFileInfo) Sys() any           { return nil }
func (m mockFileInfo) Mode() os.FileMode {
	if m.isCharDevice {
		return os.ModeCharDevice
	}
	return 0
}

func TestGetChannels(t *testing.T) {
	origStat := osStat
	t.Cleanup(func() { osStat = origStat })

	t.Run("empty channel IDs returns empty list", func(t *testing.T) {
		cfg := &spec.Config{}
		channels, err := GetChannels(cfg, "/container/dev")
		require.NoError(t, err)
		require.Empty(t, channels)
	})

	t.Run("discovers channels using devRoot and sets HostPath using NvidiaDevRoot", func(t *testing.T) {
		hostDevRoot := "/run/nvidia/driver"
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					NvidiaDevRoot: &hostDevRoot,
				},
			},
			Imex: spec.Imex{
				ChannelIDs: []int{0, 1},
			},
		}

		containerDevRoot := "/container/driver-root/dev"
		expectedDiscoveryPath0 := filepath.Join(containerDevRoot, "/dev/nvidia-caps-imex-channels/channel0")
		expectedDiscoveryPath1 := filepath.Join(containerDevRoot, "/dev/nvidia-caps-imex-channels/channel1")
		expectedHostPath0 := filepath.Join(hostDevRoot, "/dev/nvidia-caps-imex-channels/channel0")
		expectedHostPath1 := filepath.Join(hostDevRoot, "/dev/nvidia-caps-imex-channels/channel1")

		osStat = func(name string) (os.FileInfo, error) {
			if name == expectedDiscoveryPath0 || name == expectedDiscoveryPath1 ||
				name == expectedHostPath0 || name == expectedHostPath1 {
				return mockFileInfo{isCharDevice: true}, nil
			}
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, containerDevRoot)
		require.NoError(t, err)
		require.Len(t, channels, 2)

		require.Equal(t, "0", channels[0].ID)
		require.Equal(t, filepath.Join("/dev/nvidia-caps-imex-channels", "channel0"), channels[0].Path)
		require.Equal(t, filepath.Join(hostDevRoot, "/dev/nvidia-caps-imex-channels/channel0"), channels[0].HostPath)

		require.Equal(t, "1", channels[1].ID)
		require.Equal(t, filepath.Join("/dev/nvidia-caps-imex-channels", "channel1"), channels[1].Path)
		require.Equal(t, filepath.Join(hostDevRoot, "/dev/nvidia-caps-imex-channels/channel1"), channels[1].HostPath)
	})

	t.Run("character device found only at HostPath is accepted", func(t *testing.T) {
		hostDevRoot := "/run/nvidia/driver"
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					NvidiaDevRoot: &hostDevRoot,
				},
			},
			Imex: spec.Imex{
				ChannelIDs: []int{0},
			},
		}

		expectedHostPath := filepath.Join(hostDevRoot, "/dev/nvidia-caps-imex-channels/channel0")
		osStat = func(name string) (os.FileInfo, error) {
			if name == expectedHostPath {
				return mockFileInfo{isCharDevice: true}, nil
			}
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, "/nonexistent/container/dev")
		require.NoError(t, err)
		require.Len(t, channels, 1)
		require.Equal(t, expectedHostPath, channels[0].HostPath)
	})

	t.Run("channel found only in container candidate is rejected when host path is missing", func(t *testing.T) {
		hostDevRoot := "/run/nvidia/driver"
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					NvidiaDevRoot: &hostDevRoot,
				},
			},
			Imex: spec.Imex{
				ChannelIDs: []int{0},
				Required:   true,
			},
		}

		containerDevRoot := "/container/driver-root/dev"
		expectedDiscoveryPath := filepath.Join(containerDevRoot, "/dev/nvidia-caps-imex-channels/channel0")
		osStat = func(name string) (os.FileInfo, error) {
			if name == expectedDiscoveryPath {
				return mockFileInfo{isCharDevice: true}, nil
			}
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, containerDevRoot)
		require.Error(t, err)
		require.Nil(t, channels)
	})

	t.Run("fallback to NvidiaDriverRoot if NvidiaDevRoot unset", func(t *testing.T) {
		driverRoot := "/custom/driver/root"
		cfg := &spec.Config{
			Flags: spec.Flags{
				CommandLineFlags: spec.CommandLineFlags{
					NvidiaDriverRoot: &driverRoot,
				},
			},
			Imex: spec.Imex{
				ChannelIDs: []int{0},
			},
		}

		expectedPath := filepath.Join("/dev/nvidia-caps-imex-channels", "channel0")
		expectedHostPath := filepath.Join(driverRoot, expectedPath)
		osStat = func(name string) (os.FileInfo, error) {
			if name == expectedPath || name == expectedHostPath {
				return mockFileInfo{isCharDevice: true}, nil
			}
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, "")
		require.NoError(t, err)
		require.Len(t, channels, 1)
		require.Equal(t, expectedHostPath, channels[0].HostPath)
	})

	t.Run("channel missing with required=true returns error", func(t *testing.T) {
		cfg := &spec.Config{
			Imex: spec.Imex{
				ChannelIDs: []int{99},
				Required:   true,
			},
		}
		osStat = func(name string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, "/dev")
		require.Error(t, err)
		require.Nil(t, channels)
	})

	t.Run("channel missing with required=false skips channel", func(t *testing.T) {
		cfg := &spec.Config{
			Imex: spec.Imex{
				ChannelIDs: []int{99},
				Required:   false,
			},
		}
		osStat = func(name string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		}

		channels, err := GetChannels(cfg, "/dev")
		require.NoError(t, err)
		require.Empty(t, channels)
	})
}

func TestChannelExists(t *testing.T) {
	origStat := osStat
	t.Cleanup(func() { osStat = origStat })

	t.Run("non-char device returns error", func(t *testing.T) {
		osStat = func(name string) (os.FileInfo, error) {
			return mockFileInfo{isCharDevice: false}, nil
		}
		ch := Channel{
			ID:       "0",
			Path:     "/dev/nvidia-caps-imex-channels/channel0",
			HostPath: "/host/dev/nvidia-caps-imex-channels/channel0",
		}
		exists, err := ch.exists()
		require.False(t, exists)
		require.Error(t, err)
	})

	t.Run("char device returns true", func(t *testing.T) {
		osStat = func(name string) (os.FileInfo, error) {
			return mockFileInfo{isCharDevice: true}, nil
		}
		ch := Channel{
			ID:       "0",
			Path:     "/dev/nvidia-caps-imex-channels/channel0",
			HostPath: "/host/dev/nvidia-caps-imex-channels/channel0",
		}
		exists, err := ch.exists()
		require.True(t, exists)
		require.NoError(t, err)
	})

	t.Run("validation continues past non-character candidate to accept later valid candidate", func(t *testing.T) {
		nonCharCandidate := "/dev/non-char"
		validCandidate := "/dev/valid-char"

		osStat = func(name string) (os.FileInfo, error) {
			if name == nonCharCandidate {
				return mockFileInfo{isCharDevice: false}, nil
			}
			if name == validCandidate {
				return mockFileInfo{isCharDevice: true}, nil
			}
			return nil, os.ErrNotExist
		}

		exists, err := channelExists(nonCharCandidate, validCandidate)
		require.True(t, exists)
		require.NoError(t, err)
	})
}
