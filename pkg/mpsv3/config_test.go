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
	"testing"
)

func intPtr(v int) *int {
	return &v
}

func TestMPSV3NodeConfigResolve(t *testing.T) {
	tests := []struct {
		name        string
		input       MPSV3NodeConfig
		want        MPSV3NodeConfig
		expectErr   bool
		errContains string
	}{
		{
			name:  "empty defaults to namespace-cap",
			input: MPSV3NodeConfig{},
			want: MPSV3NodeConfig{
				ComputeMode: ComputeModeNamespaceCap,
			},
			expectErr: false,
		},
		{
			name: "explicit namespace-cap valid",
			input: MPSV3NodeConfig{
				ComputeMode: ComputeModeNamespaceCap,
			},
			want: MPSV3NodeConfig{
				ComputeMode: ComputeModeNamespaceCap,
			},
			expectErr: false,
		},
		{
			name: "namespace-cap rejects timeslice setting",
			input: MPSV3NodeConfig{
				ComputeMode: ComputeModeNamespaceCap,
				Timeslice:   "short",
			},
			expectErr:   true,
			errContains: "MPS timeslice settings require server-timeslice",
		},
		{
			name: "namespace-cap rejects yield level",
			input: MPSV3NodeConfig{
				ComputeMode:         ComputeModeNamespaceCap,
				TimesliceYieldLevel: intPtr(1),
			},
			expectErr:   true,
			errContains: "MPS timeslice settings require server-timeslice",
		},
		{
			name: "server-timeslice default timeslice",
			input: MPSV3NodeConfig{
				ComputeMode: ComputeModeServerTimeslice,
			},
			want: MPSV3NodeConfig{
				ComputeMode: ComputeModeServerTimeslice,
				Timeslice:   "default",
			},
			expectErr: false,
		},
		{
			name: "server-timeslice valid timeslice and yield level 1",
			input: MPSV3NodeConfig{
				ComputeMode:         ComputeModeServerTimeslice,
				Timeslice:           "medium",
				TimesliceYieldLevel: intPtr(1),
			},
			want: MPSV3NodeConfig{
				ComputeMode:         ComputeModeServerTimeslice,
				Timeslice:           "medium",
				TimesliceYieldLevel: intPtr(1),
			},
			expectErr: false,
		},
		{
			name: "server-timeslice valid yield level 2",
			input: MPSV3NodeConfig{
				ComputeMode:         ComputeModeServerTimeslice,
				Timeslice:           "long",
				TimesliceYieldLevel: intPtr(2),
			},
			want: MPSV3NodeConfig{
				ComputeMode:         ComputeModeServerTimeslice,
				Timeslice:           "long",
				TimesliceYieldLevel: intPtr(2),
			},
			expectErr: false,
		},
		{
			name: "server-timeslice invalid timeslice value",
			input: MPSV3NodeConfig{
				ComputeMode: ComputeModeServerTimeslice,
				Timeslice:   "ultra-fast",
			},
			expectErr:   true,
			errContains: "unknown MPS timeslice",
		},
		{
			name: "server-timeslice invalid yield level",
			input: MPSV3NodeConfig{
				ComputeMode:         ComputeModeServerTimeslice,
				TimesliceYieldLevel: intPtr(3),
			},
			expectErr:   true,
			errContains: "timesliceYieldLevel must be omitted, 1 or 2",
		},
		{
			name: "unknown compute mode",
			input: MPSV3NodeConfig{
				ComputeMode: "custom-slicing",
			},
			expectErr:   true,
			errContains: "unknown MPS compute mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.Resolve()
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ComputeMode != tt.want.ComputeMode || got.Timeslice != tt.want.Timeslice {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if (got.TimesliceYieldLevel == nil) != (tt.want.TimesliceYieldLevel == nil) {
				t.Errorf("yield level mismatch: got %v, want %v", got.TimesliceYieldLevel, tt.want.TimesliceYieldLevel)
			} else if got.TimesliceYieldLevel != nil && *got.TimesliceYieldLevel != *tt.want.TimesliceYieldLevel {
				t.Errorf("yield level value mismatch: got %d, want %d", *got.TimesliceYieldLevel, *tt.want.TimesliceYieldLevel)
			}
		})
	}
}
