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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

type releaseStep struct {
	Uses string `json:"uses"`
	Run  string `json:"run"`
}

type releaseJob struct {
	Needs []string      `json:"needs"`
	Steps []releaseStep `json:"steps"`
}

type releaseWorkflow struct {
	Jobs        map[string]releaseJob `json:"jobs"`
	Concurrency struct {
		Group            string `json:"group"`
		CancelInProgress bool   `json:"cancel-in-progress"`
	} `json:"concurrency"`
}

func loadWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "helm-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func findStep(t *testing.T, workflow releaseWorkflow, match func(releaseStep) bool) (string, releaseStep) {
	t.Helper()
	for id, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if match(step) {
				return id, step
			}
		}
	}
	t.Fatal("release step not found")
	return "", releaseStep{}
}

func dependsOn(workflow releaseWorkflow, job, dependency string) bool {
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(current string) bool {
		if seen[current] {
			return false
		}
		seen[current] = true
		for _, need := range workflow.Jobs[current].Needs {
			if need == dependency || visit(need) {
				return true
			}
		}
		return false
	}
	return visit(job)
}

func TestPublishingJobsAreIndependent(t *testing.T) {
	workflow := loadWorkflow(t)
	pages, _ := findStep(t, workflow, func(step releaseStep) bool {
		return strings.HasPrefix(step.Uses, "helm/chart-releaser-action@")
	})
	oci, _ := findStep(t, workflow, func(step releaseStep) bool {
		return strings.Contains(step.Run, "helm push")
	})
	if pages == oci || dependsOn(workflow, oci, pages) {
		t.Fatal("a chart-releaser failure would prevent OCI publication")
	}
	validation, _ := findStep(t, workflow, func(step releaseStep) bool {
		return strings.Contains(step.Run, "GITHUB_REF_NAME")
	})
	if !dependsOn(workflow, pages, validation) || !dependsOn(workflow, oci, validation) {
		t.Fatal("both publishers must wait for version validation")
	}
	if workflow.Concurrency.Group == "" || workflow.Concurrency.CancelInProgress {
		t.Fatal("release runs must be serialized without cancelling an active publisher")
	}
}

const fakeHelm = `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${HELM_CALLS}"
case "$1" in
  show)
    if [[ "$3" != oci://* ]]; then
      printf 'apiVersion: v2\nname: volcano-vgpu-device-plugin\nversion: %s\n' "${LOCAL_CHART_VERSION}"
      exit 0
    fi
    case "${LOOKUP_STATUS}" in
      exists) printf 'version: %s\n' "${CHART_VERSION}"; exit 0 ;;
      missing) echo "Error: ${3#oci://}:${CHART_VERSION//+/_}: not found" >&2 ;;
      manifest_unknown) echo 'Error: MANIFEST_UNKNOWN: manifest unknown' >&2 ;;
      http_404) echo "Error: unexpected status from HEAD request to https://ghcr.io/v2/project-hami/charts/volcano-vgpu-device-plugin/manifests/${CHART_VERSION//+/_}: 404 Not Found" >&2 ;;
      denied) echo 'Error: 403 Forbidden' >&2 ;;
      credentials) echo 'Error: credentials not found' >&2 ;;
      timeout) echo 'Error: context deadline exceeded' >&2 ;;
      rate_limit) echo 'Error: 429 Too Many Requests' >&2 ;;
      *) exit 2 ;;
    esac
    exit 1
    ;;
  package)
    mkdir -p "$4"
    touch "$4/volcano-vgpu-device-plugin-${CHART_VERSION}.tgz"
    ;;
  push) ;;
  *) exit 2 ;;
esac
`

func runStep(t *testing.T, script string, values map[string]string) (string, string, string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helm"), []byte(fakeHelm), 0o700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	output := filepath.Join(dir, "output")
	env := map[string]string{
		"PATH":                    dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HELM_CALLS":              calls,
		"GITHUB_OUTPUT":           output,
		"GITHUB_REF_TYPE":         "branch",
		"GITHUB_REF_NAME":         "main",
		"GITHUB_REPOSITORY_OWNER": "Project-HAMi",
		"RUNNER_TEMP":             dir,
		"CHART_VERSION":           "1.13.0",
		"LOCAL_CHART_VERSION":     "1.13.0",
		"LOOKUP_STATUS":           "exists",
	}
	for key, value := range values {
		env[key] = value
	}
	command := exec.Command("bash", "-eu", "-o", "pipefail", "-c", script)
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		if _, overridden := env[key]; !overridden {
			command.Env = append(command.Env, value)
		}
	}
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	log, err := command.CombinedOutput()
	callBytes, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	outputBytes, readErr := os.ReadFile(output)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return string(log), string(callBytes), string(outputBytes), err
}

func TestTagVersionValidation(t *testing.T) {
	_, step := findStep(t, loadWorkflow(t), func(step releaseStep) bool {
		return strings.Contains(step.Run, "GITHUB_REF_NAME")
	})
	cases := []struct {
		name, refType, refName, version string
		valid                           bool
	}{
		{"matching_tag", "tag", "v1.13.0", "1.13.0", true},
		{"old_chart_on_new_tag", "tag", "v1.13.0", "1.12.0", false},
		{"tag_without_prefix", "tag", "1.13.0", "1.13.0", false},
		{"manual_branch", "branch", "main", "1.13.0", true},
		{"prerelease", "tag", "v1.14.0-rc.1+build.7", "1.14.0-rc.1+build.7", true},
		{"missing_version", "branch", "main", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			log, _, output, err := runStep(t, step.Run, map[string]string{
				"GITHUB_REF_TYPE": test.refType, "GITHUB_REF_NAME": test.refName,
				"LOCAL_CHART_VERSION": test.version,
			})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t, error=%v, log=%s", test.valid, err, log)
			}
			if test.valid && output != "version="+test.version+"\n" {
				t.Fatalf("unexpected validated version: %q", output)
			}
			if !test.valid && output != "" {
				t.Fatalf("invalid version exposed to publishers: %q", output)
			}
		})
	}
}

func TestOCIPublicationGuard(t *testing.T) {
	_, step := findStep(t, loadWorkflow(t), func(step releaseStep) bool {
		return strings.Contains(step.Run, "helm push")
	})
	cases := []struct {
		status         string
		valid, publish bool
	}{
		{"exists", true, false},
		{"missing", true, true},
		{"manifest_unknown", true, true},
		{"http_404", true, true},
		{"denied", false, false},
		{"credentials", false, false},
		{"timeout", false, false},
		{"rate_limit", false, false},
	}
	for _, test := range cases {
		t.Run(test.status, func(t *testing.T) {
			log, calls, _, err := runStep(t, step.Run, map[string]string{"LOOKUP_STATUS": test.status})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t, error=%v, log=%s", test.valid, err, log)
			}
			if !strings.HasPrefix(calls, "show chart oci://ghcr.io/project-hami/charts/volcano-vgpu-device-plugin --version 1.13.0\n") {
				t.Fatalf("missing version-specific registry lookup: %s", calls)
			}
			packaged := strings.Contains(calls, "\npackage ")
			pushed := strings.Contains(calls, "\npush ")
			if packaged != test.publish || pushed != test.publish {
				t.Fatalf("publish=%t, calls=%s", test.publish, calls)
			}
			if pushed && !strings.HasSuffix(strings.TrimSpace(calls), ".tgz oci://ghcr.io/project-hami/charts") {
				t.Fatalf("incorrect publication destination: %s", calls)
			}
		})
	}
}
