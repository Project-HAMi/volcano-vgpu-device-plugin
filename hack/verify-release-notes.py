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
from html.parser import HTMLParser
from pathlib import Path
import re
import sys

from markdown_it import MarkdownIt
from markdown_it.common.html_blocks import block_names


class HTMLText(HTMLParser):
    """Read displayed HTML text while ignoring comments and code/hidden blocks."""

    hidden_tags = ("script", "style", "template", "pre", "head", "title")
    boundary_tags = frozenset(block_names) | {"pre"}

    def __init__(self):
        super().__init__()
        self.parts = []
        self.hidden = 0

    def handle_starttag(self, tag, attrs):
        if tag == "br" or tag in self.boundary_tags:
            self.parts.append(" ")
        if tag in self.hidden_tags:
            self.hidden += 1

    def handle_endtag(self, tag):
        if tag in self.boundary_tags:
            self.parts.append(" ")
        if tag in self.hidden_tags:
            self.hidden = max(0, self.hidden - 1)

    def handle_data(self, data):
        if not self.hidden:
            self.parts.append(data)


def html_text(token):
    parser = HTMLText()
    parser.feed(token.content)
    parser.close()
    return "".join(parser.parts)


def inline_text(token):
    """Read displayed text and inline code, excluding HTML and link destinations."""
    parser = HTMLText()
    for child in token.children or []:
        if child.type in ("text", "code_inline"):
            parser.handle_data(child.content)
        elif child.type == "html_inline":
            parser.feed(child.content)
        elif child.type in ("softbreak", "hardbreak"):
            parser.handle_data(" ")
    parser.close()
    return "".join(parser.parts)


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
    tokens = MarkdownIt("commonmark").parse(changelog.read_text(encoding="utf-8"))
    for index, token in enumerate(tokens):
        # A closed fence spans its opening, content lines and closing line.
        if token.type == "fence" and token.map[1] - token.map[0] != token.content.count("\n") + 2:
            raise ValueError(f"{changelog} contains an unclosed code block")
        for part in [token, *(token.children or [])]:
            if part.type in ("html_block", "html_inline") and part.content.lstrip().startswith("<!--") and "-->" not in part.content:
                raise ValueError(f"{changelog} contains an unclosed HTML comment")
        if token.type == "heading_open" and token.tag in ("h1", "h2") and token.level == 0:
            current = [] if token.tag == "h2" and inline_text(tokens[index + 1]) == heading else None
            if current is not None:
                entries.append(current)
        elif current is not None:
            current.append(token)
    if len(entries) != 1:
        raise ValueError(f"expected exactly one '## {heading}' entry in {changelog}, found {len(entries)}")
    summaries = []
    prose = []
    item = None
    bullet_list = False
    for token in entries[0]:
        if token.type == "bullet_list_open" and token.level == 0:
            bullet_list = True
        elif token.type == "bullet_list_close" and token.level == 0:
            bullet_list = False
        elif token.type == "list_item_open" and token.level == 1 and bullet_list:
            item = []
        elif token.type == "list_item_close" and token.level == 1 and item is not None:
            summaries.append(" ".join(item))
            item = None
        elif token.type in ("inline", "html_block"):
            text = inline_text(token) if token.type == "inline" else html_text(token)
            prose.append(text)
            if item is not None:
                item.append(text)
    if not any(any(character.isalnum() for character in summary) for summary in summaries):
        raise ValueError(f"'## {heading}' must contain human-reviewed summary bullets")
    entry_text = "\n".join(prose)
    if re.search(r"\b(TODO|TBD|PLACEHOLDER)\b", entry_text, re.IGNORECASE):
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
