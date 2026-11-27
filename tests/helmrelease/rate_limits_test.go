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

package helmrelease

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestHelmClientRateLimits(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	tests := []struct {
		name, values, qps, burst string
		args                     []string
	}{
		{name: "defaults", qps: "5", burst: "10"},
		{name: "missing parent", args: []string{"--set", "kubeAPI=null"}, qps: "5", burst: "10"},
		{name: "missing burst", args: []string{"--set", "kubeAPI.burst=null", "--set", "kubeAPI.qps=20"}, qps: "20", burst: "10"},
		{name: "missing qps", args: []string{"--set", "kubeAPI.qps=null", "--set", "kubeAPI.burst=50"}, qps: "5", burst: "50"},
		{name: "zero", values: "kubeAPI:\n  qps: 0\n  burst: 0\n", qps: "0", burst: "0"},
		{name: "values file integer", values: "kubeAPI:\n  qps: 1.5\n  burst: 1000000\n", qps: "1.5", burst: "1000000"},
		{name: "numeric scientific notation", values: "kubeAPI:\n  burst: 1e+06\n", qps: "5", burst: "1000000"},
		{name: "integral float", values: "kubeAPI:\n  burst: 1000000.0\n", qps: "5", burst: "1000000"},
		{name: "largest safe integer", values: "kubeAPI:\n  burst: 9007199254740991\n", qps: "5", burst: "9007199254740991"},
		{name: "set integer", args: []string{"--set", "kubeAPI.burst=1000000"}, qps: "5", burst: "1000000"},
		{name: "fractional burst", values: "kubeAPI:\n  burst: 10.5\n", qps: "5", burst: "10.5"},
		{name: "invalid string", values: "kubeAPI:\n  burst: many\n", qps: "5", burst: "many"},
		{name: "quoted scientific notation", values: "kubeAPI:\n  burst: '1e+06'\n", qps: "5", burst: "1e+06"},
		{name: "quoted large integer", values: "kubeAPI:\n  burst: '9007199254740993'\n", qps: "5", burst: "9007199254740993"},
		{name: "numeric unsafe integer", values: "kubeAPI:\n  burst: 9007199254740993\n", qps: "5", burst: "9.007199254740992e+15"},
	}
	chart := filepath.Join("..", "..", "deployments", "helm", "volcano-vgpu-device-plugin")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"template", "rate-test", chart}, tt.args...)
			if tt.values != "" {
				args = append(args, "-f", "-")
			}
			cmd := exec.Command("helm", args...)
			cmd.Stdin = strings.NewReader(tt.values)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, out)
			}
			decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
			found := map[string]string{}
			for {
				var obj struct {
					Kind     string `json:"kind"`
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
					Spec struct {
						Template struct {
							Spec struct {
								Containers []struct {
									Name string `json:"name"`
									Env  []struct {
										Name  string `json:"name"`
										Value string `json:"value"`
									} `json:"env"`
								} `json:"containers"`
							} `json:"spec"`
						} `json:"template"`
					} `json:"spec"`
				}
				err := decoder.Decode(&obj)
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					t.Fatalf("decode Helm output: %v", err)
				}
				if obj.Kind != "DaemonSet" || obj.Metadata.Name != "volcano-device-plugin" {
					continue
				}
				for _, ctr := range obj.Spec.Template.Spec.Containers {
					if ctr.Name != "volcano-device-plugin" {
						continue
					}
					for _, env := range ctr.Env {
						if env.Name == "KUBE_API_QPS" || env.Name == "KUBE_API_BURST" {
							found[env.Name] = env.Value
						}
					}
				}
			}
			if found["KUBE_API_QPS"] != tt.qps || found["KUBE_API_BURST"] != tt.burst {
				t.Fatalf("got %v, want QPS %q burst %q", found, tt.qps, tt.burst)
			}
		})
	}
}
