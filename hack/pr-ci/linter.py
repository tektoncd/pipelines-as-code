"""Lints PR title, description, and commits for conventional format and completeness."""

from __future__ import annotations

import functools
import re
from pathlib import Path

from comments import PR_TITLE_COMMENT_MARKER, CommentManager
from github import GitHubClient
from pr_data import PRData

DEFAULT_JIRA_PROJECT = r"(SRVKP|KONFLUX)"
MIN_DESCRIPTION_LINES = 3
DEFAULT_PR_TEMPLATE_PATH = Path(".github/pull_request_template.md")
GITHUB_ISSUE_PATTERN = re.compile(
    r"(fixes|closes|resolves)\s+(?:[\w.-]+/[\w.-]+)?#?\d+",
    re.IGNORECASE,
)

JIRA_URL_PATTERN = re.compile(
    rf"https://issues\.redhat\.com/browse/{DEFAULT_JIRA_PROJECT}-\d+",
    re.IGNORECASE,
)

AI_KEYWORDS = (
    "gemini",
    "chatgpt",
    "gpt",
    "claude",
    "cursor",
    "copilot",
    "codeium",
    "phind",
    "llama",
    "deepseek",
    "tabnine",
    "blackbox",
    "codewhisperer",
)

# Conventional commit types we accept in PR titles
CONVENTIONAL_TYPES: tuple[str, ...] = (
    "build",
    "chore",
    "ci",
    "docs",
    "deps",
    "enhance",
    "feat",
    "dnm",
    "fix",
    "perf",
    "refactor",
    "release",
    "revert",
    "style",
    "test",
)

# Regex adapted from commitizen default specification
CONVENTIONAL_TITLE_RE = re.compile(
    r"^(?P<type>{types})(\([^)\s]+\))?(?P<breaking>!)?:\s+.+".format(
        types="|".join(CONVENTIONAL_TYPES)
    )
)


class PRLinter:
    """Lints PR title, description, and commits for conventional format and completeness."""

    def __init__(self, pr_data: PRData, github: GitHubClient):
        self.pr_data = pr_data
        self.github = github
        self.comment_manager = CommentManager(github)
        self.warnings: list[tuple[str, list[str]]] = []

    def check_all(self) -> None:
        """Run all lint checks."""
        self.check_title()
        self.check_description()
        self.check_template_usage()
        self.check_jira_reference()
        self.check_ai_attribution()

    def check_title(self) -> None:
        """Check if PR title follows conventional commit format."""
        pr_title = self.pr_data.title
        print(f"Linting PR title: {pr_title}")
        is_valid, message = is_conventional_title(pr_title)

        if is_valid:
            print("PR title matches conventional commits format")
        else:
            print(f"PR title validation warning: {message}")
            title_lines = [f"**Current title:** `{pr_title}`"]
            if message:
                title_lines.append("")
                title_lines.append(message)
            title_lines.extend(
                [
                    "",
                    "**Expected pattern:** `<type>(<scope>): <subject>`",
                    "**Allowed types:** " + ", ".join(CONVENTIONAL_TYPES),
                    "",
                    "Examples:",
                    "- `fix(controller): ensure reconciler handles nil spec`",
                    "- `docs: update contributing guide with lint instructions`",
                ]
            )
            self.warnings.append(("PR title format", title_lines))

    def check_description(self) -> None:
        """Check if PR description has enough content."""
        pr_body = self.pr_data.description
        non_empty_lines = [line for line in pr_body.splitlines() if line.strip()]

        if len(non_empty_lines) < MIN_DESCRIPTION_LINES:
            print(
                f"PR description warning: fewer than {MIN_DESCRIPTION_LINES} non-empty lines"
            )
            description_lines = [
                "Please expand the PR description with a brief summary, context, and testing notes.",
                f"Aim for at least {MIN_DESCRIPTION_LINES} meaningful lines so reviewers have enough detail.",
            ]
            self.warnings.append(("PR description completeness", description_lines))

    def check_template_usage(self) -> None:
        """Check if PR description was customized from template."""
        normalized_pr = _sanitize_template_lines(self.pr_data.description)
        default_template = _sanitize_template_lines(get_default_pr_template())

        if default_template:
            custom_lines = [
                line for line in normalized_pr if line not in default_template
            ]
            if not custom_lines:
                print("PR description warning: appears unchanged from template")
                template_lines = [
                    "The PR description still matches the default template.",
                    "Please replace the placeholder sections with project-specific details before review.",
                ]
                self.warnings.append(("PR template usage", template_lines))

    def check_jira_reference(self) -> None:
        """Check if PR has Jira or GitHub issue reference."""
        pr_body = self.pr_data.description
        has_jira = has_required_jira_reference(pr_body)
        has_github = has_github_issue_reference(pr_body)

        if has_jira:
            print("PR description contains a Jira reference")
        elif has_github:
            print("PR description has a GitHub issue reference; skipping Jira reminder")
        else:
            print("PR description warning: missing Jira reference")
            jira_lines = [
                "Add a Jira reference in the description using one of the following formats:",
                "- `https://issues.redhat.com/browse/SRVKP-<number>`",
                "",
                "If no SRVKP ticket exists yet, link a GitHub issue instead (e.g., `Fixes #123`).",
                "Minor housekeeping PRs without Jira coverage can skip this after confirming with reviewers.",
            ]
            self.warnings.append(("Jira reference", jira_lines))

    def check_ai_attribution(self) -> None:
        """Check if commits have AI attribution footers."""
        commits_data = self.github.get_pr_commits()
        missing_ai_commits: list[tuple[str, str]] = []

        for commit in commits_data:
            sha = (commit.get("sha") or "")[:7]
            message = commit.get("commit", {}).get("message", "")
            summary = message.splitlines()[0] if message else ""

            # Skip merge commits
            if len(commit.get("parents", []) or []) > 1 and summary.startswith("Merge"):
                continue

            if not commit_has_ai_footer(message):
                missing_ai_commits.append((sha, summary))

        if missing_ai_commits:
            print(
                f"AI attribution warning: {len(missing_ai_commits)} commit(s) missing "
                "Assisted-by/Co-authored-by footers"
            )
            ai_lines: list[str] = [
                "The following commits lack an explicit AI attribution footer:",
            ]
            for sha, summary in missing_ai_commits:
                label = f"{sha} `{summary.strip()}`"
                ai_lines.append(f"- {label}")
            ai_lines.extend(
                [
                    "",
                    "If no AI assistance was used for a commit, you can ignore this warning.",
                    "Otherwise add an `Assisted-by:` or `Co-authored-by:` footer referencing the AI used.",
                ]
            )
            self.warnings.append(("AI attribution", ai_lines))
        else:
            print("All commits include AI attribution footers or matching keywords")

    def report(self) -> None:
        """Post or remove lint feedback comment."""
        existing_comment = self.comment_manager.find_lint_comment()

        if not self.warnings:
            if existing_comment:
                self.comment_manager.delete_comment(existing_comment.get("id"))
                print("Removed previous lint comment since all lint checks now pass")
            return

        # Create a prettier comment with emojis and better formatting
        emoji_map = {
            "PR title format": "📝",
            "PR description completeness": "📄",
            "PR template usage": "📋",
            "Jira reference": "🎫",
            "AI attribution": "🤖",
        }

        comment_lines = [
            PR_TITLE_COMMENT_MARKER,
            "## 🔍 PR Lint Feedback",
            "",
            "> **Note**: This automated check helps ensure your PR follows our contribution guidelines.",
            "",
            "### ⚠️ Items that need attention:",
            "",
        ]

        for heading, lines in self.warnings:
            emoji = emoji_map.get(heading, "⚠️")
            comment_lines.append(f"### {emoji} {heading}")
            comment_lines.append("")

            # Add content in a nice blockquote or code block format
            for line in lines:
                if line.startswith(("**", "-")):
                    comment_lines.append(line)
                elif line == "":
                    comment_lines.append("")
                else:
                    comment_lines.append(f"> {line}")
            comment_lines.append("")
            comment_lines.append("---")
            comment_lines.append("")

        # Add footer with helpful info
        comment_lines.extend(
            [
                "### ℹ️ Next Steps",
                "",
                "- Review and address the items above",
                "- Push new commits to update this PR",
                "- This comment will be automatically updated when issues are resolved",
                "",
                "<details>",
                "<summary>🔧 <strong>Admin Tools</strong> (click to expand)</summary>",
                "",
                "**Automated Issue/Ticket Creation:**",
                "",
                "- **`/issue-create`** - Generate a GitHub issue from this PR content using AI",
                "- **`/jira-create`** - Create a SRVKP Jira ticket from this PR content using AI",
                "",
                "> ⚠️ **Important**: Always review and edit generated content before finalizing tickets/issues.",
                "> The AI-generated content should be used as a starting point and may need adjustments.",
                "",
                "*These commands are available to maintainers and will post the generated content as PR comments for review.*",
                "",
                "</details>",
                "",
                "<sub>🤖 *This feedback was generated automatically by the PR CI system*</sub>",
            ]
        )

        body = "\n".join(comment_lines)
        self.comment_manager.upsert_comment(body, existing_comment)


def _sanitize_template_lines(text: str) -> list[str]:
    if not text:
        return []
    without_comments = re.sub(r"<!--.*?-->", "", text, flags=re.DOTALL)
    cleaned_lines: list[str] = []
    for line in without_comments.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        normalized = re.sub(r"[[xX]]", "[ ]", stripped)
        cleaned_lines.append(normalized)
    return cleaned_lines


@functools.lru_cache(maxsize=1)
def get_default_pr_template() -> str:
    try:
        return DEFAULT_PR_TEMPLATE_PATH.read_text(encoding="utf-8")
    except OSError as exc:
        print(
            "Warning: Could not read default PR template"
            f" at {DEFAULT_PR_TEMPLATE_PATH}: {exc}"
        )
        return ""


def is_conventional_title(title: str) -> tuple[bool, str | None]:
    """Validate PR title against the conventional commit format."""
    if not title:
        return False, "PR title is empty"

    if not CONVENTIONAL_TITLE_RE.match(title):
        expected_types = ", ".join(CONVENTIONAL_TYPES)
        return (
            False,
            f"Expected format `<type>(<scope>): <subject>` with `<type>` one of [{expected_types}].",
        )

    subject = title.split(":", maxsplit=1)[1].strip()
    if len(subject) > 72:
        return False, "Subject should be 72 characters or fewer."

    return True, None


def has_required_jira_reference(description: str) -> bool:
    """Check whether PR description contains a valid Jira reference."""
    if not description:
        return False

    return bool(JIRA_URL_PATTERN.search(description))


def commit_has_ai_footer(message: str) -> bool:
    """Return True if commit message references AI assistance."""
    if not message:
        return False

    lines = [line.strip() for line in message.splitlines() if line.strip()]
    for line in lines:
        lower = line.lower()
        if lower.startswith(("assisted-by:", "ai-assisted-by:")) and any(
            keyword in lower for keyword in AI_KEYWORDS
        ):
            return True
        if lower.startswith("co-authored-by:") and any(
            keyword in lower for keyword in AI_KEYWORDS
        ):
            return True
    return False


def has_github_issue_reference(description: str) -> bool:
    if not description:
        return False
    return bool(GITHUB_ISSUE_PATTERN.search(description))
