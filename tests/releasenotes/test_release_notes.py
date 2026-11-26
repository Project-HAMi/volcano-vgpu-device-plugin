# Copyright 2026 The HAMi Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Exercise the release checker with only the Python standard library available."""

from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest


CHECKER = Path(__file__).resolve().parents[2] / "hack" / "verify-release-notes.py"


class ReleaseNotesTest(unittest.TestCase):
    def test_requested_entry(self):
        cases = [
            ("lowercase placeholder", "plugin", "v2.0.0", "## v2.0.0\n- Handle placeholder pods.\n", True),
            ("chart prerelease", "chart", "2.0.0-rc.1+build.2", "## Helm chart 2.0.0-rc.1+build.2\n- Update image.\n", True),
            ("literal chart version", "chart", "v2.3.4", "## Helm chart v2.3.4\n- Update image.\n", True),
            ("other chart version", "chart", "v2.3.4", "## Helm chart 2.3.4\n- Older chart.\n", False),
            ("other kind", "plugin", "v2.0.0", "## Helm chart 2.0.0\n- Update image.\n", False),
            ("empty bullet", "plugin", "v2.0.0", "## v2.0.0\n-   \n", False),
            ("next section", "plugin", "v2.0.0", "## v2.0.0\n## v1.0.0\n- Older change.\n", False),
            ("duplicate heading", "plugin", "v2.0.0", "## v2.0.0\n- First.\n## v2.0.0\n- Second.\n", False),
            ("unfinished entry", "plugin", "v2.0.0", "## v2.0.0\n- Fix allocation.\n  **TODO**: describe impact.\n", False),
        ]
        with TemporaryDirectory() as directory:
            changelog = Path(directory) / "CHANGELOG.md"
            for name, kind, version, notes, expected in cases:
                with self.subTest(name=name):
                    changelog.write_text(notes, encoding="utf-8")
                    result = subprocess.run(
                        [sys.executable, "-I", "-S", str(CHECKER), "--kind", kind,
                         "--version", version, "--changelog", str(changelog)],
                        capture_output=True, text=True, check=False,
                    )
                    self.assertEqual(result.returncode, 0 if expected else 1, result.stderr)
                    if not expected:
                        self.assertIn("Release notes check failed", result.stderr)


if __name__ == "__main__":
    unittest.main()
