"""Configuration for PR CI operations."""

from __future__ import annotations

import os
from dataclasses import dataclass, field

DEFAULT_MODEL = "gemini-2.5-flash-lite"

# JIRA Custom Fields
JIRA_CUSTOM_FIELDS = {
    "git_pr": "customfield_12310220",  # Git PR URL field
    "release_note": "customfield_12317313",  # Release Note field
}


@dataclass
class Config:
    """Configuration for PR CI operations."""

    github_token: str
    repo_owner: str
    repo_name: str
    pr_number: str
    gemini_api_key: str = ""
    gemini_model: str = DEFAULT_MODEL
    max_labels: int = -1
    excluded_labels: set[str] = field(
        default_factory=lambda: {
            "good-first-issue",
            "help-wanted",
            "wontfix",
            "hack",
        }
    )
    # JIRA configuration
    jira_endpoint: str = ""
    jira_token: str = ""
    jira_project: str = ""
    jira_component: str = ""
    jira_issuetype: str = "Story"

    @classmethod
    def from_env(cls, require_gemini: bool = False) -> Config | None:
        """Create config from environment variables."""
        github_token = os.environ.get("GITHUB_TOKEN", "")
        repo_owner = os.environ.get("REPO_OWNER", "")
        repo_name = os.environ.get("REPO_NAME", "")
        pr_number = os.environ.get("PR_NUMBER", "")

        if not all([github_token, repo_owner, repo_name, pr_number]):
            return None

        gemini_api_key = os.environ.get("GEMINI_API_KEY", "")
        if require_gemini and not gemini_api_key:
            return None

        gemini_model = os.environ.get("GEMINI_MODEL", DEFAULT_MODEL)
        max_labels = int(os.environ.get("MAX_LABELS", "-1"))

        excluded_env = os.environ.get(
            "EXCLUDED_LABELS", "good-first-issue,help-wanted,wontfix,hack"
        )
        excluded_labels = {
            label.strip() for label in excluded_env.split(",") if label.strip()
        }

        # JIRA configuration
        jira_endpoint = os.environ.get("JIRA_ENDPOINT", "")
        jira_token = os.environ.get("JIRA_TOKEN", "")
        jira_project = os.environ.get("JIRA_PROJECT", "")
        jira_component = os.environ.get("JIRA_COMPONENT", "")
        jira_issuetype = os.environ.get("JIRA_ISSUETYPE", "Story")

        return cls(
            github_token=github_token,
            repo_owner=repo_owner,
            repo_name=repo_name,
            pr_number=pr_number,
            gemini_api_key=gemini_api_key,
            gemini_model=gemini_model,
            max_labels=max_labels,
            excluded_labels=excluded_labels,
            jira_endpoint=jira_endpoint,
            jira_token=jira_token,
            jira_project=jira_project,
            jira_component=jira_component,
            jira_issuetype=jira_issuetype,
        )

    def build_jira_custom_fields(
        self, pr_url: str, release_note: str
    ) -> dict[str, str]:
        """Build custom fields dictionary for JIRA ticket creation."""
        custom_fields = {}

        # Git PR URL field
        if pr_url:
            custom_fields[JIRA_CUSTOM_FIELDS["git_pr"]] = pr_url

        # Release Note field
        if release_note:
            custom_fields[JIRA_CUSTOM_FIELDS["release_note"]] = release_note

        return custom_fields
