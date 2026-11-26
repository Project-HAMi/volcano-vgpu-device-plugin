#!/usr/bin/env python3
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

"""Require a reviewed changelog entry before publishing a release."""

import argparse
from pathlib import Path
import re
import sys


def verify(changelog, version, kind):
    title = "v" + version.removeprefix("v") if kind == "plugin" else f"Helm chart {version}"
    heading = f"## {title}"
    lines = changelog.read_text(encoding="utf-8").splitlines()
    matches = [i for i, line in enumerate(lines) if line.rstrip() == heading]
    if len(matches) != 1:
        raise ValueError(f"expected one '{heading}' in {changelog}, found {len(matches)}")

    has_bullet = False
    for line in lines[matches[0] + 1:]:
        if re.match(r"^ {0,3}#{1,2}(?:[ \t]|$)", line):
            break
        if re.search(r"\b_*(?:TODO|TBD|PLACEHOLDER)_*\b", line):
            raise ValueError(f"'{heading}' contains an unfinished note")
        if re.match(r"^ {0,3}[-*+][ \t]+\S", line):
            has_bullet = True
    if not has_bullet:
        raise ValueError(f"'{heading}' needs a nonempty summary bullet")
    return title


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", choices=("plugin", "chart"), required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--changelog", type=Path, default=Path("CHANGELOG.md"))
    args = parser.parse_args()
    try:
        heading = verify(args.changelog, args.version, args.kind)
    except (OSError, ValueError) as error:
        print(f"Release notes check failed: {error}", file=sys.stderr)
        return 1
    print(f"Release notes verified: {heading}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
