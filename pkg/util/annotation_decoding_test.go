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

package util

import (
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestDecodeNodeDevicesRejectsInvalidFields(t *testing.T) {
	for _, bad := range []string{
		"GPU-bad,invalid,4096,NVIDIA,true:",
		"GPU-bad,2147483648,4096,NVIDIA,true:",
		"GPU-bad,10,invalid,NVIDIA,true:",
		"GPU-bad,10,2147483648,NVIDIA,true:",
		"GPU-bad,10,4096,NVIDIA,invalid:",
	} {
		t.Run(bad, func(t *testing.T) {
			got := DecodeNodeDevices(bad + "GPU-good,10,4096,NVIDIA,true:")
			if len(got) != 1 || got[0].Id != "GPU-good" || got[0].Count != 10 || got[0].Devmem != 4096 || !got[0].Health {
				t.Fatalf("invalid entry must be skipped and the valid entry preserved: %+v", got)
			}
		})
	}
}

func TestDecodePodDevicesRejectsInvalidFieldsWithoutReducingCount(t *testing.T) {
	for _, bad := range []string{
		"GPU-bad,NVIDIA,invalid,50:",
		"GPU-bad,NVIDIA,2147483648,50:",
		"GPU-bad,NVIDIA,128,invalid:",
		"GPU-bad,NVIDIA,128,2147483648:",
	} {
		t.Run(bad, func(t *testing.T) {
			annotation := bad + "GPU-good,NVIDIA,128,50:"
			got, count := DecodePodDevices(annotation)
			if count != 2 || len(got) != 1 || len(got[0]) != 1 || got[0][0].UUID != "GPU-good" || got[0][0].Usedmem != 128 || got[0][0].Usedcores != 50 {
				t.Fatalf("decoded devices = %+v, count = %d; want only the valid device and count 2", got, count)
			}
			pod := pendingPod("gpu-pod", "node", annotation, nil)
			matched, err := getOldestPod([]v1.Pod{pod}, "node", 1)
			if err != nil || matched != nil {
				t.Fatalf("malformed two-device request must not match one device: pod=%v error=%v", matched, err)
			}
		})
	}
}
