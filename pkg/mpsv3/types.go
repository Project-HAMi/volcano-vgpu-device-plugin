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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	cdspec "tags.cncf.io/container-device-interface/specs-go"
)

const (
	ContainerPipePath = "/run/hami-mps"
	StateVersion      = 620
	CDIKind           = "hami.io/mps-v3"
	CDIVersion        = "0.6.0"
)

// Allocation records the active or in-flight MPS v3 resource configuration for a container.
type Allocation struct {
	Version        int           `json:"version,omitempty"`
	ComputeMode    string        `json:"computeMode,omitempty"`
	SMPolicy       MPSV3SMPolicy `json:"smPolicy,omitzero"`
	PodUID         string        `json:"podUID"`
	Container      string        `json:"container"`
	GPUs           []string      `json:"gpus"`
	Memory         int64         `json:"memoryMiB"`
	Percent        int64         `json:"percent"`
	SMCount        int64         `json:"smCount,omitempty"`
	Prepared       bool          `json:"prepared"`
	CleanupPending bool          `json:"cleanupPending,omitempty"`
	Server         string        `json:"server"`
	Namespace      string        `json:"namespace"`
}

// ServerName computes the deterministic server name for this allocation.
func (a Allocation) ServerName() string {
	key := strings.Join(a.GPUs, ",")
	if a.ComputeMode == ComputeModeServerTimeslice {
		key = a.PodUID + "/" + a.Container + "/" + key
	}
	return HashName("srv", key)
}

// PartitionName computes the deterministic static SM partition name on a specific GPU.
func (a Allocation) PartitionName(gpu string) string {
	return HashName("sm", a.Namespace+"/"+gpu)
}

// HashName generates a 12-byte hex hash prefix string safe for CLI arguments and socket paths.
func HashName(prefix, key string) string {
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s_%x", prefix, sum[:12])
}

// Envs returns the environment variables required inside the client container.
func (a Allocation) Envs() map[string]string {
	return map[string]string{
		"NVIDIA_GPU_MEMORY_REQUEST": strconv.FormatInt(a.Memory, 10),
		"NVIDIA_GPU_MEMORY_LIMIT":   strconv.FormatInt(a.Memory, 10),
		"CUDA_MPS_PIPE_DIRECTORY":   ContainerPipePath,
	}
}

// CDISpec builds the CDI specification for this allocation.
func (a Allocation) CDISpec(root string) ([]byte, error) {
	envs := a.Envs()
	var edits []string
	for k, v := range envs {
		edits = append(edits, k+"="+v)
	}
	slices.Sort(edits)

	spec := cdspec.Spec{
		Version: CDIVersion,
		Kind:    CDIKind,
		Devices: []cdspec.Device{
			{
				Name: a.Namespace,
				ContainerEdits: cdspec.ContainerEdits{
					Env: edits,
					Mounts: []*cdspec.Mount{
						{
							HostPath:      filepath.Join(root, "pipe", a.Server, a.Namespace),
							ContainerPath: ContainerPipePath,
							Options:       []string{"bind", "ro"},
						},
					},
				},
			},
		},
	}
	return json.Marshal(spec)
}

// atomicMPSFile atomically writes data to destination path via temp file and directory fsync.
func atomicMPSFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".mps-v3-*")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if err = f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() {
		_ = d.Close()
	}()
	return d.Sync()
}
