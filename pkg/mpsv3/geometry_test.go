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

	corev1 "k8s.io/api/core/v1"
)

func TestGeometryPartitionAndPercent(t *testing.T) {
	// Ada / Ampere: 108 SMs, chunk = 2
	ampere := MPSV3Geometry{SMs: 108, Chunk: 2}

	// Exact chunk match
	chunks, rem, err := ampere.Partition(10)
	if err != nil || chunks != 5 || rem {
		t.Fatalf("unexpected partition result: chunks=%d, rem=%v, err=%v", chunks, rem, err)
	}
	pct, err := ampere.Percent(10)
	if err != nil || pct != 10 { // (10*100 + 107)/108 = 1107/108 = 10
		t.Fatalf("unexpected percent result: %d, err: %v", pct, err)
	}

	// Odd SM count on chunk-2 device (not allowed unless matching physical remainder)
	_, _, err = ampere.Partition(11)
	if err == nil {
		t.Fatalf("expected error for unrepresentable SM count 11 on chunk 2")
	}

	// Hopper / Blackwell discrete: 132 SMs, chunk = 8 (132 % 8 = 4)
	hopper := MPSV3Geometry{SMs: 132, Chunk: 8}
	chunksH, remH, err := hopper.Partition(16)
	if err != nil || chunksH != 2 || remH {
		t.Fatalf("unexpected hopper partition: chunks=%d, rem=%v, err=%v", chunksH, remH, err)
	}

	// Remainder match: 20 SMs -> 16 + 4 (remainder 4 matches 132 % 8 = 4)
	chunksR, remR, err := hopper.Partition(20)
	if err != nil || chunksR != 2 || !remR {
		t.Fatalf("expected remainder partition: chunks=%d, rem=%v, err=%v", chunksR, remR, err)
	}

	// Remainder mismatch: 18 SMs -> 16 + 2 (remainder 2 does not match 4)
	_, _, err = hopper.Partition(18)
	if err == nil {
		t.Fatalf("expected error for hopper partition with remainder 2")
	}
}

func TestSMPolicyValidate(t *testing.T) {
	geom := MPSV3Geometry{SMs: 108, Chunk: 2}

	// Standard fixed partition
	policy := MPSV3SMPolicy{}
	if err := policy.Validate(geom, 20); err != nil {
		t.Fatalf("unexpected error validating fixed policy: %v", err)
	}

	// Elastic policy with valid chunk
	elastic := MPSV3SMPolicy{Shareable: true, MaxChunks: "25"}
	if err := elastic.Validate(geom, 20); err != nil {
		t.Fatalf("unexpected error validating elastic policy: %v", err)
	}

	// MaxChunks smaller than base chunks (base chunks = 10)
	invalidMax := MPSV3SMPolicy{MaxChunks: "5"}
	if err := invalidMax.Validate(geom, 20); err == nil {
		t.Fatalf("expected error when MaxChunks is smaller than base chunks")
	}

	// MaxChunks exceeding device total chunks (108 / 2 = 54)
	overflowMax := MPSV3SMPolicy{MaxChunks: "60"}
	if err := overflowMax.Validate(geom, 20); err == nil {
		t.Fatalf("expected error when MaxChunks exceeds total device chunks")
	}
}

func TestParseRequest(t *testing.T) {
	// Standard percentage namespace-cap request
	mode, sms, policy, err := ParseRequest(nil)
	if err != nil || mode != ComputeModeNamespaceCap || sms != 0 || policy.Elastic() {
		t.Fatalf("unexpected default parse request: mode=%s, sms=%d, err=%v", mode, sms, err)
	}

	// Static SM request with shareable and max chunks
	annos := map[string]string{
		MPSV3SMCountAnnotation:     "24",
		MPSV3SMShareableAnnotation: "true",
		MPSV3SMMaxChunksAnnotation: "20",
	}
	mode, sms, policy, err = ParseRequest(annos)
	if err != nil || mode != ComputeModeNamespaceCap || sms != 24 || !policy.Shareable || policy.MaxChunks != "20" {
		t.Fatalf("unexpected parsed static SM request: mode=%s, sms=%d, policy=%+v, err=%v", mode, sms, policy, err)
	}

	// Static SM requested on server-timeslice (must fail)
	badAnnos := map[string]string{
		MPSV3ComputeModeAnnotation: ComputeModeServerTimeslice,
		MPSV3SMCountAnnotation:     "24",
	}
	_, _, _, err = ParseRequest(badAnnos)
	if err == nil {
		t.Fatalf("expected error requesting static SMs on server-timeslice")
	}
}

func TestValidateContainerEnvironment(t *testing.T) {
	validCtr := &corev1.Container{
		Name: "test-app",
		Env: []corev1.EnvVar{
			{Name: "APP_ENV", Value: "prod"},
		},
	}
	if err := ValidateContainerEnvironment(validCtr); err != nil {
		t.Fatalf("unexpected error for valid container: %v", err)
	}

	reservedCtr := &corev1.Container{
		Name: "bad-app",
		Env: []corev1.EnvVar{
			{Name: "CUDA_MPS_PIPE_DIRECTORY", Value: "/tmp/fake"},
		},
	}
	if err := ValidateContainerEnvironment(reservedCtr); err == nil {
		t.Fatalf("expected error when container sets reserved MPS env var")
	}
}
