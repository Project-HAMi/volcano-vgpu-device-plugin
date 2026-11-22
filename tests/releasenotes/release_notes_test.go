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

package releasenotes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

type notesStep struct {
	Name string            `json:"name"`
	Uses string            `json:"uses"`
	Run  string            `json:"run"`
	Env  map[string]string `json:"env"`
}

type notesJob struct {
	Needs []string    `json:"needs"`
	Steps []notesStep `json:"steps"`
}

type notesWorkflow struct {
	Jobs map[string]notesJob `json:"jobs"`
}

func workflow(t *testing.T, filename string) notesWorkflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", filename))
	if err != nil {
		t.Fatal(err)
	}
	var result notesWorkflow
	if err := yaml.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func guard(t *testing.T, job notesJob) (notesStep, int) {
	t.Helper()
	for index, step := range job.Steps {
		if strings.Contains(step.Run, "hack/verify-release-notes.py") {
			return step, index
		}
	}
	t.Fatal("publishing workflow has no release-notes gate")
	return notesStep{}, -1
}

func fixture(t *testing.T, notes string) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "hack"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "hack", "verify-release-notes.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "hack", "verify-release-notes.py"), script, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "CHANGELOG.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestReleaseNotesBlockPublishing(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		job      string
		heading  string
		variable string
		version  string
	}{
		{"image", "release-image-build.yml", "build", "v1.14.0", "GITHUB_REF_NAME", "v1.14.0"},
		{"chart", "helm-release.yml", "validate-chart", "Helm chart 1.14.0", "CHART_VERSION", "1.14.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flow := workflow(t, test.filename)
			job := flow.Jobs[test.job]
			step, gateIndex := guard(t, job)
			if test.name == "image" {
				for index, candidate := range job.Steps {
					if (strings.HasPrefix(candidate.Uses, "docker/login-action@") || strings.Contains(candidate.Run, "make push")) && gateIndex >= index {
						t.Fatal("release notes must be checked before registry credentials and publishing")
					}
				}
			} else {
				if step.Env["CHART_VERSION"] != "${{ steps.version.outputs.version }}" {
					t.Fatal("chart notes must use the validated chart version")
				}
				for _, publisher := range []string{"helm-release", "oci-release"} {
					needs := flow.Jobs[publisher].Needs
					validated := false
					for _, dependency := range needs {
						validated = validated || dependency == test.job
					}
					if !validated {
						t.Fatalf("%s must depend on the release-notes validation job", publisher)
					}
				}
			}
			entries := []struct {
				name  string
				notes string
				ready bool
			}{
				{"missing", "", false},
				{"empty bullet", "- \n", false},
				{"inline placeholder", "- Improve scheduling (TODO: describe impact).\n", false},
				{"formatted placeholder", "- **TBD**\n", false},
				{"HTML comment only", "<!--\n- Done.\n-->\n", false},
				{"indented code only", "\n    - Example.\n", false},
				{"empty link", "- [](#details)\n", false},
				{"list continuation", "- Fixes allocation.\n    <!-- TODO: internal note. -->\n", true},
				{"unequal backtick runs", "- Handles `a ``` <!-- b` correctly.\n", true},
				{"reviewed", "- Adds a reviewed feature and describes upgrade impact.\n", true},
			}
			for _, entry := range entries {
				notes := "# Release notes\n\n## v1.13.0\n\n- Existing release.\n"
				if entry.notes != "" {
					notes += "\n## " + test.heading + "\n\n" + entry.notes
				}
				t.Run(entry.name, func(t *testing.T) {
					directory := fixture(t, notes)
					marker := filepath.Join(directory, "published")
					command := exec.Command("bash", "-eu", "-o", "pipefail", "-c", step.Run+"\nprintf published > \"$PUBLISH_MARKER\"\n")
					command.Dir = directory
					command.Env = append(os.Environ(), test.variable+"="+test.version, "PUBLISH_MARKER="+marker)
					output, err := command.CombinedOutput()
					_, markerErr := os.Stat(marker)
					if entry.ready && (err != nil || markerErr != nil) {
						t.Fatalf("reviewed release was blocked: %v, marker: %v\n%s", err, markerErr, output)
					}
					if !entry.ready && (err == nil || !os.IsNotExist(markerErr)) {
						t.Fatalf("incomplete release notes reached publishing: %v, marker: %v\n%s", err, markerErr, output)
					}
				})
			}
		})
	}
}

func TestReleaseNotesEntryValidation(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		version string
		notes   string
		valid   bool
	}{
		{"plugin", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n", true},
		{"chart", "chart", "1.14.0", "## Helm chart 1.14.0\n- Updates image versions.\n", true},
		{"prerelease", "plugin", "v1.14.0-rc.1+build.2", "## v1.14.0-rc.1+build.2\n- Adds a release candidate.\n", true},
		{"empty", "plugin", "v1.14.0", "## v1.14.0\n", false},
		{"empty bullet", "plugin", "v1.14.0", "## v1.14.0\n- \n", false},
		{"whitespace bullets", "plugin", "v1.14.0", "## v1.14.0\n  - \t \n*   \n", false},
		{"markup-only bullets", "plugin", "v1.14.0", "## v1.14.0\n- ** **\n- ` `\n", false},
		{"mixed bullets", "plugin", "v1.14.0", "## v1.14.0\n- \n- Fixes allocation.\n* \t\n", true},
		{"links only", "plugin", "v1.14.0", "## v1.14.0\n[Changes](https://example.org)\n", false},
		{"placeholder", "plugin", "v1.14.0", "## v1.14.0\n- TBD\n", false},
		{"inline TODO", "plugin", "v1.14.0", "## v1.14.0\n- Improve scheduling (TODO: describe impact).\n", false},
		{"inline lowercase TBD", "plugin", "v1.14.0", "## v1.14.0\n- Adds a feature (tbd).\n", false},
		{"trailing PLACEHOLDER", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation. PLACEHOLDER\n", false},
		{"continued bullet placeholder", "plugin", "v1.14.0", "## v1.14.0\n- Improves scheduling.\n  TODO: describe upgrade impact.\n", false},
		{"bold TBD", "plugin", "v1.14.0", "## v1.14.0\n- **TBD**\n", false},
		{"italic TODO", "plugin", "v1.14.0", "## v1.14.0\n- *TODO*\n", false},
		{"underscore PLACEHOLDER", "plugin", "v1.14.0", "## v1.14.0\n- __PLACEHOLDER__\n", false},
		{"inline code TODO", "plugin", "v1.14.0", "## v1.14.0\n- `TODO`\n", false},
		{"nested formatting", "plugin", "v1.14.0", "## v1.14.0\n- Improves scheduling (**`TBD`**).\n", false},
		{"substantive Markdown", "plugin", "v1.14.0", "## v1.14.0\n- **Fixes allocation** with `runtimeClassName` enabled.\n", true},
		{"marker substrings", "plugin", "v1.14.0", "## v1.14.0\n- Corrects todo_count and placeholder_count defaults and removes obsolete TODOs.\n", true},
		{"duplicate", "plugin", "v1.14.0", "## v1.14.0\n- First.\n## v1.14.0\n- Second.\n", false},
		{"code example", "plugin", "v1.14.0", "```markdown\n## v1.14.0\n- Example.\n```\n", false},
		{"long code fence", "plugin", "v1.14.0", "````markdown\n```\n## v1.14.0\n- Example.\n````\n", false},
		{"unclosed code fence", "plugin", "v1.14.0", "```markdown\n## v1.14.0\n- Example.\n", false},
		{"fenced bullet", "plugin", "v1.14.0", "## v1.14.0\n```markdown\n- Example only.\n```\n", false},
		{"example plus release", "plugin", "v1.14.0", "```markdown\n## v1.14.0\n- Example.\n```\n## v1.14.0\n- Fixes allocation.\n", true},
		{"fenced placeholder example", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n```markdown\n- TBD\n```\n", true},
		{"HTML comment only", "plugin", "v1.14.0", "## v1.14.0\n<!--\n- Done.\n-->\n", false},
		{"hidden heading", "plugin", "v1.14.0", "<!--\n## v1.14.0\n- Done.\n-->\n", false},
		{"hidden duplicate heading", "plugin", "v1.14.0", "<!--\n## v1.14.0\n- Example.\n-->\n## v1.14.0\n- Fixes allocation.\n", true},
		{"comment-only bullet", "plugin", "v1.14.0", "## v1.14.0\n- <!-- Done. -->\n", false},
		{"inline comment and summary", "plugin", "v1.14.0", "## v1.14.0\n- <!-- Reviewed. -->Fixes allocation.\n", true},
		{"hidden placeholder", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n<!--\n- TODO\n-->\n", true},
		{"inline hidden placeholder", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation. <!-- TODO: internal note. -->\n", true},
		{"comment then visible summary", "plugin", "v1.14.0", "## v1.14.0\n<!-- Internal\nnote. -->\n- Fixes allocation.\n", true},
		{"fenced comment syntax", "plugin", "v1.14.0", "```markdown\n<!--\n- Example.\n```\n## v1.14.0\n- Fixes allocation.\n", true},
		{"inline code comment syntax", "plugin", "v1.14.0", "## v1.14.0\n- Documents `<!--` comment syntax.\n", true},
		{"indented code comment syntax", "plugin", "v1.14.0", "## v1.14.0\n\n    <!-- literal syntax\n\t<!-- literal syntax\n  \t<!-- literal syntax\n\n- Fixes allocation.\n", true},
		{"fence inside HTML comment", "plugin", "v1.14.0", "<!--\n```markdown\n- Example.\n-->\n## v1.14.0\n- Fixes allocation.\n", true},
		{"unclosed HTML comment", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n<!-- Internal note.\n", false},
		{"indented code only", "plugin", "v1.14.0", "## v1.14.0\n\n    - Example.\n", false},
		{"tab-indented code only", "plugin", "v1.14.0", "## v1.14.0\n\n\t- Example.\n", false},
		{"three-space summary", "plugin", "v1.14.0", "## v1.14.0\n\n   - Fixes allocation.\n", true},
		{"indented code and summary", "plugin", "v1.14.0", "## v1.14.0\n\n    - Example.\n\n- Fixes allocation.\n", true},
		{"list continuation comment", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n    <!-- TODO: internal note. -->\n", true},
		{"nested list continuation comment", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n    - Improves registration.\n        <!-- TODO: internal note. -->\n", true},
		{"indented code within list", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n\n      <!-- literal syntax\n\n- Improves registration.\n", true},
		{"paragraph ends list", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n\nConfiguration example:\n\n    <!-- literal syntax\n\n- Improves registration.\n", true},
		{"unequal single backtick runs", "plugin", "v1.14.0", "## v1.14.0\n- Handles `a ``` <!-- b` correctly.\n", true},
		{"unequal double backtick runs", "plugin", "v1.14.0", "## v1.14.0\n- Handles ``a ``` <!-- b`` correctly.\n", true},
		{"empty link", "plugin", "v1.14.0", "## v1.14.0\n- [](#details)\n", false},
		{"whitespace link label", "plugin", "v1.14.0", "## v1.14.0\n- [ ](#details)\n", false},
		{"empty titled link", "plugin", "v1.14.0", "## v1.14.0\n- [](#details \"Release details\")\n", false},
		{"readable link", "plugin", "v1.14.0", "## v1.14.0\n- [Fixes allocation](https://example.org)\n", true},
		{"marker in link destination", "plugin", "v1.14.0", "## v1.14.0\n- [Fixes allocation](https://example.org/TODO)\n", true},
		{"empty link with summary", "plugin", "v1.14.0", "## v1.14.0\n- [](#details) Fixes allocation.\n", true},
		{"empty link with nested destination", "plugin", "v1.14.0", "## v1.14.0\n- [](https://example.org/a(b))\n", false},
		{"nested readable link label", "plugin", "v1.14.0", "## v1.14.0\n- [Fixes [GPU] allocation](https://example.org/a(b))\n", true},
		{"unclosed fence after summary", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n```\nExample.\n", false},
		{"literal fence in code example", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation.\n```\n    ```\n```\n", true},
		{"nested release heading", "plugin", "v1.14.0", "- Example:\n\n  ## v1.14.0\n\n  - Example only.\n", false},
		{"placeholder link label", "plugin", "v1.14.0", "## v1.14.0\n- [**TODO**](#details)\n", false},
		{"script-only bullet", "plugin", "v1.14.0", "## v1.14.0\n- <script>Done.</script>\n", false},
		{"inline script placeholder", "plugin", "v1.14.0", "## v1.14.0\n- Fixes allocation. <script>TODO</script>\n", true},
		{"HTML code-only bullet", "plugin", "v1.14.0", "## v1.14.0\n- <pre>Example only.</pre>\n", false},
		{"neighbor entry", "plugin", "v1.14.0", "## v1.14.0\n\n## v1.15.0\n- Other release.\n", false},
		{"invalid version", "plugin", "main", "## main\n- Other branch.\n", false},
		{"leading zero", "plugin", "v01.14.0", "## v01.14.0\n- Other version.\n", false},
		{"prerelease zero", "plugin", "v1.14.0-01", "## v1.14.0-01\n- Other version.\n", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("python3", "hack/verify-release-notes.py", "--kind", test.kind, "--version", test.version)
			command.Dir = fixture(t, test.notes)
			output, err := command.CombinedOutput()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v\n%s", test.valid, err, output)
			}
		})
	}
}
