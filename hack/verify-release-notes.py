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
    version = version.removeprefix("v")
    if not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", version):
        raise ValueError("release version must use MAJOR.MINOR.PATCH with optional prerelease/build identifiers")
    if "-" in version.split("+", 1)[0]:
        prerelease = version.split("+", 1)[0].split("-", 1)[1]
        if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in prerelease.split(".")):
            raise ValueError("numeric prerelease identifiers must not have leading zeroes")
    heading = "v" + version if kind == "plugin" else "Helm chart " + version
    entries = []
    current = None
    fence = None
    for line in changelog.read_text(encoding="utf-8").splitlines():
        fence_match = re.match(r"^\s*(`{3,}|~{3,})", line)
        if fence_match:
            marker = fence_match[1]
            if fence is None:
                fence = (marker[0], len(marker))
            elif marker[0] == fence[0] and len(marker) >= fence[1] and not line[fence_match.end():].strip():
                fence = None
            continue
        if fence is not None:
            continue
        header = re.fullmatch(r"##\s+(.+?)\s*", line)
        if header:
            current = [] if header[1] == heading else None
            if current is not None:
                entries.append(current)
        elif current is not None:
            current.append(line)
    if fence is not None:
        raise ValueError(f"{changelog} contains an unclosed code block")
    if len(entries) != 1:
        raise ValueError(f"expected exactly one '## {heading}' entry in {changelog}, found {len(entries)}")
    summaries = [line.strip()[2:].strip() for line in entries[0] if re.match(r"^\s*[-*]\s+", line)]
    if not summaries:
        raise ValueError(f"'## {heading}' must contain human-reviewed summary bullets")
    if any(re.match(r"^(TODO|TBD|PLACEHOLDER)\b", item, re.IGNORECASE) for item in summaries):
        raise ValueError(f"'## {heading}' still contains a placeholder")
    return heading


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
