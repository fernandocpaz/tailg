"""Choose a fresh stable tag; never move an existing published version."""

import re
import subprocess
from pathlib import Path


def parse_version(value):
    match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value)
    return tuple(map(int, match.groups())) if match else None


def next_version(tags, requested):
    desired = parse_version(requested.strip())
    if desired is None:
        raise ValueError(".github/RELEASE must contain a stable vMAJOR.MINOR.PATCH")
    highest = max((v for tag in tags if (v := parse_version(tag))), default=None)
    if highest is None or desired > highest:
        selected = desired
    else:
        major, minor, patch = highest
        selected = (major, minor, patch + 1)
    return "v" + ".".join(map(str, selected))


if __name__ == "__main__":
    tags = subprocess.check_output(["git", "tag", "--list"], text=True).splitlines()
    print(next_version(tags, Path(".github/RELEASE").read_text()))
