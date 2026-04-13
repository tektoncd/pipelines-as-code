#!/usr/bin/env python3
"""Parse gotestsum JSON output and generate a GitHub Step Summary in markdown."""

import collections
import json
import sys


def main():
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} <json-file> <provider-name>", file=sys.stderr)
        sys.exit(1)

    json_file = sys.argv[1]
    target = sys.argv[2]

    results = {}  # test_name -> {action, elapsed}
    test_output = collections.defaultdict(list)

    with open(json_file) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            test = entry.get("Test")
            if not test:
                continue
            action = entry.get("Action", "")
            if action == "output":
                test_output[test].append(entry.get("Output", ""))
            elif action in ("pass", "fail", "skip"):
                results[test] = {
                    "action": action,
                    "elapsed": entry.get("Elapsed", 0),
                }

    passed = sorted([t for t, r in results.items() if r["action"] == "pass"])
    failed = sorted([t for t, r in results.items() if r["action"] == "fail"])
    skipped = sorted([t for t, r in results.items() if r["action"] == "skip"])

    # Filter to top-level tests only (no subtests) for counts
    top_passed = [t for t in passed if "/" not in t]
    top_failed = [t for t in failed if "/" not in t]
    top_skipped = [t for t in skipped if "/" not in t]

    status = ":x: FAILED" if top_failed else ":white_check_mark: PASSED"

    lines = []
    lines.append(f"## E2E Tests: {target} {status}")
    lines.append("")
    lines.append(
        f":white_check_mark: **{len(top_passed)}** passed"
        f" | :x: **{len(top_failed)}** failed"
        f" | :fast_forward: **{len(top_skipped)}** skipped"
    )
    lines.append("")

    if top_failed:
        lines.append("### Failed Tests")
        lines.append("")
        for t in top_failed:
            elapsed = results[t]["elapsed"]
            lines.append(f"<details><summary>:x: {t} ({elapsed:.1f}s)</summary>")
            lines.append("")
            lines.append("```")
            output = "".join(test_output.get(t, []))
            # Include subtest output too
            for sub, r in sorted(results.items()):
                if sub.startswith(t + "/") and r["action"] == "fail":
                    output += "".join(test_output.get(sub, []))
            lines.append(output.rstrip())
            lines.append("```")
            lines.append("")
            lines.append("</details>")
            lines.append("")

    if top_passed:
        lines.append("<details><summary>Passed Tests</summary>")
        lines.append("")
        for t in top_passed:
            elapsed = results[t]["elapsed"]
            lines.append(f"- :white_check_mark: {t} ({elapsed:.1f}s)")
        lines.append("")
        lines.append("</details>")
        lines.append("")

    if top_skipped:
        lines.append("<details><summary>Skipped Tests</summary>")
        lines.append("")
        for t in top_skipped:
            lines.append(f"- :fast_forward: {t}")
        lines.append("")
        lines.append("</details>")

    print("\n".join(lines))


if __name__ == "__main__":
    main()
