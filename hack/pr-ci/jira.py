"""JIRA integration for PR CI operations."""

from __future__ import annotations

import json
from typing import Any

import requests
from google import genai
from pr_data import PRData

from config import DEFAULT_MODEL, Config


class JiraClient:
    """Handles creating tickets in JIRA."""

    def __init__(self, config: Config):
        """Initialize JIRA client with configuration."""
        self.config = config
        self.endpoint = config.jira_endpoint.rstrip("/")
        self.session = requests.Session()
        self.session.headers.update(
            {
                "Accept": "application/json",
                "Content-Type": "application/json",
                "Authorization": f"Bearer {config.jira_token}",
            }
        )

    def create_ticket(
        self,
        summary: str,
        description: str,
        custom_fields: dict[str, Any] | None = None,
    ) -> dict[str, Any] | None:
        """Create a new issue in JIRA."""
        api_url = f"{self.endpoint}/rest/api/2/issue"

        fields = {
            "project": {"key": self.config.jira_project},
            "summary": summary,
            "description": description,
            "issuetype": {"name": self.config.jira_issuetype},
        }

        if self.config.jira_component:
            fields["components"] = [{"name": self.config.jira_component}]

        # Add custom fields if provided
        if custom_fields:
            fields.update(custom_fields)

        payload = {"fields": fields}

        try:
            response = self.session.post(api_url, json=payload, timeout=30)
            response.raise_for_status()
            return response.json()
        except requests.exceptions.RequestException as e:
            print(f"Failed to create JIRA ticket: {e}")
            if hasattr(e, "response") and e.response is not None:
                print(f"Response: {e.response.text}")
            return None


class GeminiJiraGenerator:
    """Generates JIRA tickets using Gemini AI."""

    def __init__(self, api_key: str, model_name: str = DEFAULT_MODEL):
        """Initialize with Gemini credentials."""
        self.client = genai.Client(api_key=api_key)
        self.model_name = model_name

    def generate_jira_ticket(
        self,
        pr_data: PRData,
        user_query: str = "",
    ) -> dict[str, str] | None:
        """Generate JIRA ticket content for a PR."""
        # Use SRVKP JIRA template from the project rules
        jira_template = """h1. Story (Required)

As a <PERSONA> trying to <ACTION> I want <THIS OUTCOME>

_<Describes high level purpose and goal for this story. Answers the questions: Who is impacted, what is it and why do we need it? How does it improve the customer's experience?>_

h2. *Background (Required)*

_<Describes the context or background related to this story>_

h2. *Out of scope*

_<Defines what is not included in this story>_

h2. *Approach (Required)*

_<Description of the general technical path on how to achieve the goal of the story. Include details like json schema, class definitions>_

h2. *Dependencies*

_<Describes what this story depends on. Dependent Stories and EPICs should be linked to the story.>_

h2. *Acceptance Criteria (Mandatory)*

_<Describe edge cases to consider when implementing the story and defining tests>_

_<Provides a required and minimum list of acceptance tests for this story. More is expected as the engineer implements this story>_

h1. *INVEST Checklist*

Dependencies identified

Blockers noted and expected delivery timelines set

Design is implementable

Acceptance criteria agreed upon

Story estimated

h4. *Legend*

Unknown

Verified

Unsatisfied

h2. *Done Checklist*

* Code is completed, reviewed, documented and checked in
* Unit and integration test automation have been delivered and running cleanly in continuous integration/staging/canary environment
* Continuous Delivery pipeline(s) is able to proceed with new code included
* Customer facing documentation, API docs etc. are produced/updated, reviewed and published
* Acceptance criteria are met

h2. *Original Pull Request*

[{pr_url}|{pr_url}]

h3. *Original Pull Request Description*

{pr_description}"""

        # Format PR description and files for context
        pr_description = pr_data.description or "No description provided"
        files_summary = self._format_files_summary(pr_data.files_changed)

        prompt = f"""You are an expert JIRA ticket creator for the Pipelines as Code project.
Generate a JIRA ticket from this pull request information.

**IMPORTANT**: You must respond with valid JSON in this exact format:
{{
    "title": "Brief title for the JIRA ticket",
    "description": "Full JIRA ticket description using the template below"
}}

User query/context: {user_query or "Create a JIRA ticket for this pull request"}

**Pull Request Information:**
- URL: {pr_data.url}
- Title: {pr_data.title}
- Description: {pr_description}
- Author: {pr_data.author}
- Files changed: {len(pr_data.files_changed)} files
{files_summary}

**Recent commits:**
{self._format_commits(pr_data.commit_messages)}

**Template to fill (use JIRA text formatting):**
{jira_template}

Generate a meaningful JIRA ticket that:
1. Has a clear, concise title
2. Fills out the template appropriately based on the PR content
3. Uses proper JIRA text formatting (h1., h2., *bold*, _italic_, [link|url])
4. Includes the PR link in the "Original Pull Request" section
5. Provides meaningful acceptance criteria based on the changes

Respond only with the JSON object."""

        try:
            response = self.client.models.generate_content(
                model=self.model_name, contents=prompt
            )
            if not response:
                return None

            # Parse JSON response
            response_text = response.text.strip()
            if response_text.startswith("```json"):
                response_text = response_text[7:-3]
            elif response_text.startswith("```"):
                response_text = response_text[3:-3]

            parsed = json.loads(response_text)

            if (
                not isinstance(parsed, dict)
                or "title" not in parsed
                or "description" not in parsed
            ):
                print(f"Invalid JSON structure in Gemini response: {parsed}")
                return None

            return {
                "title": str(parsed["title"]).strip(),
                "description": str(parsed["description"]).strip(),
            }

        except json.JSONDecodeError as e:
            print(f"Failed to parse Gemini JSON response: {e}")
            print(f"Response was: {response.text}")
            return None
        except Exception as e:  # noqa: BLE001
            print(f"Error generating JIRA ticket: {e}")
            return None

    def _format_files_summary(self, files: list[str]) -> str:
        """Format changed files for the prompt."""
        if not files:
            return ""

        # Group files by type/directory
        categories = {}
        for file in files[:20]:  # Limit to avoid prompt bloat
            if file.startswith("pkg/"):
                category = "Core Package Changes"
            elif file.startswith("test/"):
                category = "Test Changes"
            elif file.startswith("docs/"):
                category = "Documentation Changes"
            elif file.endswith((".yaml", ".yml")):
                category = "Configuration Changes"
            elif file.endswith(".go"):
                category = "Go Code Changes"
            else:
                category = "Other Changes"

            if category not in categories:
                categories[category] = []
            categories[category].append(file)

        summary = "\n**File Categories:**\n"
        for category, file_list in categories.items():
            summary += f"- {category}: {len(file_list)} files\n"
            if len(file_list) <= 5:
                for file in file_list:
                    summary += f"  - {file}\n"
            else:
                for file in file_list[:3]:
                    summary += f"  - {file}\n"
                summary += f"  - ... and {len(file_list) - 3} more\n"

        return summary

    def _format_commits(self, commits: list[str]) -> str:
        """Format commit messages for the prompt."""
        if not commits:
            return "No commits available"

        formatted = []
        for commit in commits[-5:]:
            # Truncate long commit messages
            if len(commit) > 100:
                commit = commit[:97] + "..."
            formatted.append(f"- {commit}")

        return "\n".join(formatted)

    def generate_release_note(self, pr_data: PRData) -> str | None:
        """Generate a Red Hat style release note from PR data."""
        prompt = f"""Generate a concise 3-line release note for this pull request following Red Hat documentation style.

**Guidelines:**
- Maximum 3 lines
- Focus on user-facing benefits and functionality
- Use clear, professional language
- Highlight key changes or fixes
- Avoid technical implementation details
- Start with action verbs when possible
- Be specific about what changed/improved

**Pull Request Information:**
- Title: {pr_data.title}
- Description: {pr_data.description or "No description provided"}
- Author: {pr_data.author}

**Recent commits:**
{self._format_commits(pr_data.commit_messages)}

Generate a release note that clearly communicates the value of this change to end users. Respond with only the release note text, no additional formatting or explanation."""

        try:
            response = self.client.models.generate_content(
                model=self.model_name, contents=prompt
            )
            if not response:
                return None

            # Clean and validate the response
            release_note = response.text.strip()

            # Ensure it's not too long (3 lines max, ~200 chars per line)
            lines = release_note.split("\n")
            if len(lines) > 3:
                release_note = "\n".join(lines[:3])

            # Truncate if still too long
            if len(release_note) > 600:
                release_note = release_note[:597] + "..."

            return release_note

        except Exception as e:  # noqa: BLE001
            print(f"Error generating release note: {e}")
            # Fallback to PR title if generation fails
            return f"Updated functionality in {pr_data.title}"
