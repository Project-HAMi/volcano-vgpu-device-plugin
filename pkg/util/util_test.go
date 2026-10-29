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
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testDeviceConfigCMName = "volcano-vgpu-device-config"

func deviceConfigCM(namespace string, data map[string]string) *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: testDeviceConfigCMName, Namespace: namespace},
		Data:       data,
	}
}

func TestDeviceConfigNamespaces(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       []string
	}{
		{
			name:       "unset keeps legacy search order",
			configured: "",
			want:       []string{"kube-system", "volcano-system"},
		},
		{
			name:       "custom namespace is searched first",
			configured: "volcano",
			want:       []string{"volcano", "kube-system", "volcano-system"},
		},
		{
			name:       "legacy namespace is not searched twice",
			configured: "volcano-system",
			want:       []string{"volcano-system", "kube-system"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deviceConfigNamespaces(tt.configured); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("deviceConfigNamespaces(%q) = %v, want %v", tt.configured, got, tt.want)
			}
		})
	}
}

func TestLoadConfigFromCM(t *testing.T) {
	tests := []struct {
		name           string
		configured     string
		objects        []runtime.Object
		wantSplitCount uint
		wantErr        bool
	}{
		{
			name:       "found in configured namespace",
			configured: "volcano",
			objects: []runtime.Object{
				deviceConfigCM("volcano", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 7\n"}),
			},
			wantSplitCount: 7,
		},
		{
			name:       "configured namespace wins over kube-system",
			configured: "volcano",
			objects: []runtime.Object{
				deviceConfigCM("volcano", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 7\n"}),
				deviceConfigCM("kube-system", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 3\n"}),
			},
			wantSplitCount: 7,
		},
		{
			name:       "falls back to kube-system",
			configured: "volcano",
			objects: []runtime.Object{
				deviceConfigCM("kube-system", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 3\n"}),
			},
			wantSplitCount: 3,
		},
		{
			name:       "falls back to volcano-system when unset",
			configured: "",
			objects: []runtime.Object{
				deviceConfigCM("volcano-system", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 5\n"}),
			},
			wantSplitCount: 5,
		},
		{
			name:       "missing everywhere",
			configured: "volcano",
			objects:    nil,
			wantErr:    true,
		},
		{
			name:       "missing key",
			configured: "volcano",
			objects: []runtime.Object{
				deviceConfigCM("volcano", map[string]string{"other.yaml": "nvidia: {}\n"}),
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := fake.NewClientset(tt.objects...)
			got, err := loadConfigFromCM(context.Background(), cs, deviceConfigNamespaces(tt.configured), testDeviceConfigCMName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got config %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.NvidiaConfig.DeviceSplitCount != tt.wantSplitCount {
				t.Errorf("deviceSplitCount = %d, want %d", got.NvidiaConfig.DeviceSplitCount, tt.wantSplitCount)
			}
		})
	}
}

// TestLoadConfigFromCMStopsOnNonNotFoundError checks that an error other than
// NotFound in the configured namespace is returned instead of being hidden by a
// silent fallback to a legacy namespace.
func TestLoadConfigFromCMStopsOnNonNotFoundError(t *testing.T) {
	cs := fake.NewClientset(
		deviceConfigCM("kube-system", map[string]string{DeviceConfigurationConfigMapKey: "nvidia:\n  deviceSplitCount: 3\n"}),
	)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, testDeviceConfigCMName, errors.New("denied"))
	cs.PrependReactor("get", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "volcano" {
			return true, nil, forbidden
		}
		return false, nil, nil
	})

	got, err := loadConfigFromCM(context.Background(), cs, deviceConfigNamespaces("volcano"), testDeviceConfigCMName)
	if err == nil {
		t.Fatalf("expected the Forbidden error, got config %+v", got)
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("expected a Forbidden error, got %v", err)
	}
}

func TestDecodeNodeDevicesOutOfRange(t *testing.T) {
	got := DecodeNodeDevices("GPU-a,4294967297,4294967298,NVIDIA,true:")
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1", len(got))
	}
	if got[0].Count != math.MaxInt32 || got[0].Devmem != math.MaxInt32 {
		t.Fatalf("out-of-range values must clamp, not wrap: count=%d devmem=%d", got[0].Count, got[0].Devmem)
	}
}

func pendingPod(name, node, devs string, annos map[string]string) v1.Pod {
	a := map[string]string{AssignedNodeAnnotations: node, AssignedIDsToAllocateAnnotations: devs}
	for k, v := range annos {
		a[k] = v
	}
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: a},
		Status:     v1.PodStatus{Phase: v1.PodPending},
	}
}

func TestGetOldestPod(t *testing.T) {
	const (
		small = "GPU-a,NVIDIA,683,30:;"
		big   = "GPU-a,NVIDIA,1366,30:;"
	)
	at := func(vt, bt string) map[string]string {
		return map[string]string{AssignedTimeAnnotations: vt, BindTimeAnnotations: bt}
	}
	done := pendingPod("done", "n1", small, at("1", "1"))
	done.Annotations[DeviceBindPhase] = DeviceBindSuccess
	failed := pendingPod("failed", "n1", small, at("1", "1"))
	failed.Annotations[DeviceBindPhase] = DeviceBindFailed
	running := pendingPod("running", "n1", small, at("1", "1"))
	running.Status.Phase = v1.PodRunning

	tests := []struct {
		name    string
		pods    []v1.Pod
		want    string
		wantErr bool
	}{
		{"no pods", nil, "", false},
		{"other node", []v1.Pod{pendingPod("a", "n2", small, at("1", "1"))}, "", false},
		{"device count mismatch", []v1.Pod{pendingPod("a", "n1", small+"GPU-b,NVIDIA,1,1:;", at("1", "1"))}, "", false},
		{"older vgpu-time wins", []v1.Pod{
			pendingPod("new", "n1", big, at("5", "5")),
			pendingPod("old", "n1", small, at("4", "9")),
		}, "old", false},
		{"bind-time breaks vgpu-time tie", []v1.Pod{
			pendingPod("late", "n1", big, at("5", "7")),
			pendingPod("early", "n1", small, at("5", "6")),
		}, "early", false},
		{"allocation already done is skipped", []v1.Pod{
			done, failed, running,
			pendingPod("live", "n1", big, at("9", "9")),
		}, "live", false},
		{"only finished pods", []v1.Pod{done, failed, running}, "", false},
		{"tie with different devices is ambiguous", []v1.Pod{
			pendingPod("a", "n1", small, at("5", "5")),
			pendingPod("b", "n1", big, at("5", "5")),
		}, "", true},
		{"missing timestamps tie is ambiguous", []v1.Pod{
			pendingPod("a", "n1", small, nil),
			pendingPod("b", "n1", big, nil),
		}, "", true},
		{"tie with identical devices is fine", []v1.Pod{
			pendingPod("a", "n1", small, at("5", "5")),
			pendingPod("b", "n1", small, at("5", "5")),
		}, "a", false},
		{"strictly older pod resolves an earlier tie", []v1.Pod{
			pendingPod("a", "n1", small, at("5", "5")),
			pendingPod("b", "n1", big, at("5", "5")),
			pendingPod("c", "n1", big, at("4", "4")),
		}, "c", false},
		{"tie behind an older pod is irrelevant", []v1.Pod{
			pendingPod("c", "n1", big, at("4", "4")),
			pendingPod("a", "n1", small, at("5", "5")),
			pendingPod("b", "n1", big, at("5", "5")),
		}, "c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getOldestPod(tt.pods, "n1", 1)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			gotName := ""
			if got != nil {
				gotName = got.Name
			}
			if gotName != tt.want {
				t.Fatalf("got %q, want %q", gotName, tt.want)
			}
		})
	}
}
