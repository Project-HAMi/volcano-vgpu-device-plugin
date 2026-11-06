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

package mps

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
)

func TestRootPaths(t *testing.T) {
	root := Root("/tmp/mps-root")
	resource := spec.ResourceName("nvidia.com/gpu")

	require.Equal(t, filepath.Join("/tmp/mps-root", "nvidia.com/gpu", "log"), root.LogDir(resource))
	require.Equal(t, filepath.Join("/tmp/mps-root", "nvidia.com/gpu", "pipe"), root.PipeDir(resource))
	require.Equal(t, filepath.Join("/tmp/mps-root", "shm"), root.ShmDir(resource))
	require.Equal(t, filepath.Join("/tmp/mps-root", "nvidia.com/gpu", ".started"), root.StartedFile(resource))
	require.Equal(t, filepath.Join("/tmp/mps-root", "nvidia.com/gpu", ".ready"), root.ReadyFile(resource))
}

