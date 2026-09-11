"""Data about a pull request."""

from __future__ import annotations

from dataclasses import dataclass, field

from github import GitHubClient

MIRRORED_TAGS = ("🪞 [MIRRORED]", "[MIRRORED]")


@dataclass
class PRData:
    """Data about a pull request."""

    title: str
    description: str
    files_changed: list[str]
    commit_messages: list[str]
    pr_info: dict | None
    current_labels: list[str] = field(default_factory=list)

    @classmethod
    def from_github(cls, github: GitHubClient) -> PRData | None:
        """Fetch PR data from GitHub API."""
        pr_info = github.get_pr_info()
        if not pr_info:
            return None

        title = pr_info.get("title", "")
        description = pr_info.get("body", "") or ""
        files_changed = github.get_pr_files()

        commits_data = github.get_pr_commits()
        commit_messages = [
            commit.get("commit", {}).get("message", "")
            for commit in commits_data
            if commit.get("commit", {}).get("message", "")
        ]

        current_labels = [label["name"] for label in pr_info.get("labels", [])]

        return cls(
            title=title,
            description=description,
            files_changed=files_changed,
            commit_messages=commit_messages,
            pr_info=pr_info,
            current_labels=current_labels,
        )

    @property
    def number(self) -> int:
        """Get PR number from pr_info."""
        return self.pr_info.get("number", 0) if self.pr_info else 0

    @property
    def author(self) -> str:
        """Get PR author from pr_info."""
        if self.pr_info and "user" in self.pr_info:
            return self.pr_info["user"].get("login", "unknown")
        return "unknown"

    @property
    def url(self) -> str:
        """Get PR HTML URL from pr_info."""
        return self.pr_info.get("html_url", "") if self.pr_info else ""

    @property
    def comments(self) -> list[dict]:
        """Get PR comments. Currently returns empty list - would need GitHub API extension."""
        # This would require additional GitHub API calls to fetch comments
        # For now, return empty list as comments aren't currently fetched in from_github
        return []

    @property
    def is_mirrored(self) -> bool:
        """Check if this is a mirrored PR based on title."""
        return any(self.title.startswith(tag) for tag in MIRRORED_TAGS)
