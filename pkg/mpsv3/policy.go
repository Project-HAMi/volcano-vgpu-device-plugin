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
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Supported annotations for MPS v3 workloads.
const (
	MPSV3ComputeModeAnnotation = "hami.io/mps-compute-mode"
	MPSV3SMCountAnnotation     = "hami.io/mps-sm-count"
	MPSV3SMShareableAnnotation = "hami.io/mps-sm-shareable"
	MPSV3SMMaxChunksAnnotation = "hami.io/mps-sm-max-chunks"

	MPSV3GeometryKey        = "mpsV3Geometry"
	MPSV3ModeKey            = "mpsV3ComputeMode"
	MPSV3GPUSetKey          = "mpsV3GPUSet"
	MPSV3ComputeStrategyKey = "mpsV3ComputeStrategy"
)

// MPSV3SMPolicy defines the elastic and oversubscription behavior of static SM partitions.
type MPSV3SMPolicy struct {
	Shareable bool   `json:"shareable,omitempty"`
	MaxChunks string `json:"maxChunks,omitempty"`
}

// Elastic returns true if the partition can dynamically expand or share execution resources.
func (p MPSV3SMPolicy) Elastic() bool {
	return p.Shareable || p.MaxChunks != ""
}

// Validate ensures policy compatibility with the device geometry and requested static SM count.
func (p MPSV3SMPolicy) Validate(g MPSV3Geometry, sms int64) error {
	chunks, remainder, err := g.Partition(sms)
	if err != nil {
		return err
	}
	if p.Elastic() && remainder {
		return fmt.Errorf("elastic SM partition requires a whole number of chunks")
	}
	if p.MaxChunks != "" && p.MaxChunks != "unlimited" {
		maxChunks, err := strconv.ParseInt(p.MaxChunks, 10, 64)
		if err != nil || maxChunks < chunks || maxChunks > g.SMs/g.Chunk {
			return fmt.Errorf("SM max chunks %q outside [%d, %d]", p.MaxChunks, chunks, g.SMs/g.Chunk)
		}
	}
	return nil
}

// RequestedSMs extracts and parses the static SM count annotation.
func RequestedSMs(annotations map[string]string) (int64, error) {
	if annotations == nil {
		return 0, nil
	}
	raw, ok := annotations[MPSV3SMCountAnnotation]
	if !ok {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", MPSV3SMCountAnnotation)
	}
	return n, nil
}

// ParseRequest resolves the requested compute mode, static SM count, and policy from annotations.
func ParseRequest(annotations map[string]string) (mode string, sms int64, policy MPSV3SMPolicy, err error) {
	mode = ComputeModeNamespaceCap
	if annotations != nil {
		if val, ok := annotations[MPSV3ComputeModeAnnotation]; ok && val != "" {
			mode = val
		}
	}
	if mode != ComputeModeNamespaceCap && mode != ComputeModeServerTimeslice {
		return mode, 0, policy, fmt.Errorf("unknown MPS compute mode %q", mode)
	}
	sms, err = RequestedSMs(annotations)
	if err != nil {
		return mode, 0, policy, err
	}

	shareableVal, hasShareable := annotations[MPSV3SMShareableAnnotation]
	maxChunksVal, hasMaxChunks := annotations[MPSV3SMMaxChunksAnnotation]

	if (sms > 0 && mode != ComputeModeNamespaceCap) || (sms == 0 && (hasShareable || hasMaxChunks)) {
		return mode, 0, policy, fmt.Errorf("SM partition options require a static SM request in namespace-cap mode")
	}

	if hasShareable {
		switch shareableVal {
		case "true":
			policy.Shareable = true
		case "false":
			policy.Shareable = false
		default:
			return mode, 0, policy, fmt.Errorf("%s must be true or false", MPSV3SMShareableAnnotation)
		}
	}

	if hasMaxChunks {
		if maxChunksVal != "unlimited" {
			n, err := strconv.ParseUint(maxChunksVal, 10, 32)
			if err != nil {
				return mode, 0, policy, fmt.Errorf("%s must be 0, a positive integer or unlimited", MPSV3SMMaxChunksAnnotation)
			}
			maxChunksVal = ""
			if n > 0 {
				maxChunksVal = strconv.FormatUint(n, 10)
			}
		}
		policy.MaxChunks = maxChunksVal
	}

	return mode, sms, policy, nil
}

// ValidateContainerEnvironment rejects container environment variables that interfere with MPS v3.
func ValidateContainerEnvironment(ctr *corev1.Container) error {
	if ctr == nil {
		return nil
	}
	for _, env := range ctr.Env {
		if strings.HasPrefix(env.Name, "NVIDIA_GPU_MEMORY_") || strings.HasPrefix(env.Name, "CUDA_MPS_") {
			return fmt.Errorf("container %q sets reserved MPS environment variable %s", ctr.Name, env.Name)
		}
	}
	return nil
}
