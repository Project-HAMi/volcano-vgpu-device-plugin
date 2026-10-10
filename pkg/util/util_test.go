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
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"volcano.sh/k8s-device-plugin/pkg/config"
)

const testDeviceConfigCMName = "volcano-vgpu-device-config"

func deviceConfigCM(namespace string, data map[string]string) *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: testDeviceConfigCMName, Namespace: namespace},
		Data:       data,
	}
}

func TestSelectNodeConfigsPriorityAndFallback(t *testing.T) {
	selector := func(key, value string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{key: value}}
	}
	entries := []config.NodeConfig{
		{Name: "*", OperatingMode: "fallback"},
		{NodeLabelSelector: selector("gpu-pool", "large"), OperatingMode: "label-first"},
		{NodeLabelSelector: selector("zone", "a"), OperatingMode: "label-second"},
		{Name: "gpu-01", OperatingMode: "exact"},
	}
	tests := []struct {
		name   string
		node   string
		labels map[string]string
		want   []string
	}{
		{name: "exact name wins over selectors", node: "gpu-01", labels: map[string]string{"gpu-pool": "large", "zone": "a"}, want: []string{"exact"}},
		{name: "all label rules retain list order", node: "gpu-02", labels: map[string]string{"gpu-pool": "large", "zone": "a"}, want: []string{"label-first", "label-second"}},
		{name: "wildcard is only fallback", node: "gpu-03", labels: map[string]string{"zone": "b"}, want: []string{"fallback"}},
		{name: "no matching label uses wildcard", node: "gpu-04", labels: map[string]string{"zone": "b"}, want: []string{"fallback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := selectNodeConfigs(entries, tt.node, tt.labels)
			if err != nil {
				t.Fatalf("selectNodeConfigs returned error: %v", err)
			}
			got := make([]string, len(selected))
			for i := range selected {
				got[i] = selected[i].OperatingMode
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("selected modes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyNodeConfigsUsesExplicitPositiveValuesInOrder(t *testing.T) {
	memory1, memory2 := 1.5, 2.0
	cores := 1.25
	zero := 0.0
	split1, split2, splitZero := uint(4), uint(8), uint(0)
	filterEmpty := &config.FilterDevice{}
	selected := []config.NodeConfig{
		{Devicememoryscaling: &memory1, Devicecorescaling: &cores, Devicesplitcount: &split1, OperatingMode: "first"},
		{Devicememoryscaling: &memory2, Devicesplitcount: &split2, OperatingMode: ""},
		{Devicememoryscaling: &zero, Devicecorescaling: &zero, Devicesplitcount: &splitZero, OperatingMode: "", FilterDevice: filterEmpty},
	}
	oldMode, oldFilter := config.Mode, config.DevicePluginFilterDevice
	t.Cleanup(func() { config.Mode, config.DevicePluginFilterDevice = oldMode, oldFilter })
	globalFilter := &config.FilterDevice{UUID: []string{"GPU-keep"}}
	config.DevicePluginFilterDevice = globalFilter
	settings := config.NvidiaConfig{DeviceMemoryScaling: 1, DeviceCoreScaling: 1, DeviceSplitCount: 2}
	applyNodeConfigs(&settings, selected)
	if settings.DeviceMemoryScaling != memory2 || settings.DeviceCoreScaling != cores || settings.DeviceSplitCount != split2 || config.Mode != "first" {
		t.Fatalf("unexpected overlaid settings: %+v; mode=%q", settings, config.Mode)
	}
	if config.DevicePluginFilterDevice != globalFilter {
		t.Fatalf("empty filter rule cleared inherited filter: got %+v", config.DevicePluginFilterDevice)
	}
}

func TestValidateNodeConfigsRejectsMalformedRules(t *testing.T) {
	tests := []struct {
		name    string
		entries []config.NodeConfig
	}{
		{name: "missing selector", entries: []config.NodeConfig{{}}},
		{name: "name and selector conflict", entries: []config.NodeConfig{{Name: "node-1", NodeLabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"gpu": "yes"}}}}},
		{name: "empty selector", entries: []config.NodeConfig{{NodeLabelSelector: &metav1.LabelSelector{}}}},
		{name: "invalid selector", entries: []config.NodeConfig{{NodeLabelSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "gpu", Operator: "Invalid", Values: []string{"yes"}}}}}}},
		{name: "empty name remains invalid", entries: []config.NodeConfig{{Name: ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateNodeConfigs(tt.entries); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
	if err := validateNodeConfigs([]config.NodeConfig{{Name: "*"}}); err != nil {
		t.Fatalf("explicit wildcard fallback rejected: %v", err)
	}
}

func TestGetNodeLabelsReturnsLookupFailures(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-01", Labels: map[string]string{"gpu-pool": "large"}}})
	got, err := getNodeLabels(ctx, cs, "gpu-01")
	if err != nil {
		t.Fatalf("getNodeLabels returned error for existing node: %v", err)
	}
	if got["gpu-pool"] != "large" {
		t.Fatalf("node labels = %v, want gpu-pool=large", got)
	}
	if _, err := getNodeLabels(ctx, cs, "missing"); err == nil {
		t.Fatal("expected missing-node lookup to fail rather than act like empty labels")
	}
}

func TestDecodeNodeLabelSelectorConfig(t *testing.T) {
	const input = `{"nodeconfig":[{"nodelabelselector":{"matchLabels":{"gpu-pool":"large"},"matchExpressions":[{"key":"zone","operator":"In","values":["a","b"]}]},"devicesplitcount":8}]}`
	var configs config.DevicePluginConfigs
	if err := json.Unmarshal([]byte(input), &configs); err != nil {
		t.Fatalf("decode node config: %v", err)
	}
	if err := validateNodeConfigs(configs.Nodeconfig); err != nil {
		t.Fatalf("validate decoded node config: %v", err)
	}
	selected, err := selectNodeConfigs(configs.Nodeconfig, "gpu-01", map[string]string{"gpu-pool": "large", "zone": "a"})
	if err != nil || len(selected) != 1 || selected[0].Devicesplitcount == nil || *selected[0].Devicesplitcount != 8 {
		t.Fatalf("decoded label rule did not select expected config: selected=%+v err=%v", selected, err)
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
