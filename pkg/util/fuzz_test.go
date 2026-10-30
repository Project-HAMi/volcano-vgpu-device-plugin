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

import "testing"

func FuzzDecodeNodeDevices(f *testing.F) {
	f.Add("GPU-a,10,32768,NVIDIA,true:")
	f.Add("")
	f.Add(",,,,:")
	f.Add("GPU-a,10:")
	f.Fuzz(func(t *testing.T, s string) {
		DecodeNodeDevices(s)
	})
}

func FuzzDecodePodDevices(f *testing.F) {
	f.Add("GPU-a,NVIDIA,683,30:;")
	f.Add("")
	f.Add(",,,:;;,")
	f.Add("GPU-a,NVIDIA:")
	f.Fuzz(func(t *testing.T, s string) {
		DecodePodDevices(s)
	})
}

func TestDecodePodDevicesCountsMalformedEntries(t *testing.T) {
	pd, count := DecodePodDevices("GPU-a,NVIDIA,683,30:GPU-b,NVIDIA:")
	if count != 2 {
		t.Fatalf("count = %d, want 2 so a truncated entry cannot match a one-device request", count)
	}
	if len(pd) != 1 || len(pd[0]) != 1 {
		t.Fatalf("expected only the valid entry to be decoded, got %v", pd)
	}
}
