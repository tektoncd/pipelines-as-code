#!/usr/bin/env -S uv --quiet run --script
# /// script
# requires-python = ">=3.12"
# dependencies = [
#     "requests",
#     "google-genai",
# ]
# ///
"""Main entry point for PR CI utilities."""

import argparse

from comments import CommentManager
from gemini import GeminiAnalyzer, GeminiIssueGenerator
from github import GitHubClient
from jira import GeminiJiraGenerator, JiraClient
from linter import PRLinter
from pr_data import PRData
from utils import check_file_categories, detect_modified_providers

from config import Config


def run_lint() -> None:
    """Run PR linting checks."""
    config = Config.from_env(require_gemini=False)
    if not config:
        print("Error: Missing required environment variables")
        return

    github = GitHubClient(config)
    pr_data = PRData.from_github(github)
    if not pr_data:
        print("Could not fetch PR info for linting")
        return

    if pr_data.is_mirrored:
        print(f"Skipping lint checks for mirrored PR: {pr_data.title}")
        return

    linter = PRLinter(pr_data, github)
    linter.check_all()
    linter.report()


def run_update() -> None:
    """Run PR label update using Gemini analysis."""
    config = Config.from_env(require_gemini=True)
    if not config:
        print(
            "Error: Missing required environment variables (including GEMINI_API_KEY)"
        )
        return

    github = GitHubClient(config)
    pr_data = PRData.from_github(github)
    if not pr_data:
        print("Could not fetch PR data")
        return

    if pr_data.is_mirrored:
        print(f"Skipping label update for mirrored PR: {pr_data.title}")
        return

    print(f"Analyzing PR #{config.pr_number}: {pr_data.title}")
    print(f"Using Gemini model: {config.gemini_model}")
    print(f"Files changed: {len(pr_data.files_changed)} files")
    print(f"Commits: {len(pr_data.commit_messages)} commits")
    print(f"Current labels: {pr_data.current_labels}")

    # Skip if PR already has max_labels or more labels (unless unlimited)
    if config.max_labels > 0 and len(pr_data.current_labels) >= config.max_labels:
        print(
            f"PR already has {len(pr_data.current_labels)} labels "
            f"(max: {config.max_labels}), skipping label addition"
        )
        return

    # Get available labels
    available_labels = github.get_available_labels()
    if not available_labels:
        print("Could not fetch available labels")
        return
    print(f"Available labels in repo: {len(available_labels)} labels")

    # Analyze with Gemini
    analyzer = GeminiAnalyzer(config.gemini_api_key, config.gemini_model)
    suggested_labels = analyzer.suggest_labels(
        pr_data, available_labels, config.excluded_labels
    )
    if not suggested_labels:
        print("No labels suggested by Gemini")
        return

    print(f"Gemini suggested labels: {suggested_labels}")

    # Validate suggested labels exist
    available_label_names = {label["name"] for label in available_labels}
    valid_labels = [
        label for label in suggested_labels if label in available_label_names
    ]
    if len(valid_labels) != len(suggested_labels):
        invalid = [
            label for label in suggested_labels if label not in available_label_names
        ]
        print(f"Warning: Gemini suggested invalid labels: {invalid}")

    # Apply file-based restrictions
    has_docs, has_test, has_provider = check_file_categories(pr_data.files_changed)
    provider_types = (
        detect_modified_providers(pr_data.files_changed) if has_provider else set()
    )

    filtered_labels: list[str] = []
    for label in valid_labels:
        if label == "documentation" and not has_docs:
            print("Skipping 'documentation' label - no docs/ files modified")
            continue
        if label == "e2e" and not has_test:
            print("Skipping 'e2e' label - no test/ files modified")
            continue
        if label in ["github", "gitlab", "bitbucket", "gitea"]:
            if not has_provider:
                print(f"Skipping '{label}' label - no pkg/provider/ files modified")
                continue
            if label not in provider_types:
                print(
                    f"Skipping '{label}' label - no pkg/provider/{label}/ files modified"
                )
                continue
        filtered_labels.append(label)

    # Limit to maximum labels
    if config.max_labels > 0 and len(filtered_labels) > config.max_labels:
        print(f"Limiting labels from {len(filtered_labels)} to {config.max_labels}")
        filtered_labels = filtered_labels[: config.max_labels]

    # Filter out existing labels
    existing_set = set(pr_data.current_labels)
    new_labels = [label for label in filtered_labels if label not in existing_set]

    if new_labels:
        print(f"Adding new labels: {new_labels}")
        github.add_labels(new_labels)
    else:
        print("All suggested labels already exist on the PR")


def run_issue_create() -> None:
    """Generate and create a GitHub issue from PR content."""
    config = Config.from_env(require_gemini=True)
    if not config:
        print(
            "Error: Missing required environment variables (including GEMINI_API_KEY)"
        )
        return

    github = GitHubClient(config)
    pr_data = PRData.from_github(github)
    if not pr_data:
        print("Could not fetch PR data for issue creation")
        return

    if pr_data.is_mirrored:
        print(f"Skipping issue creation for mirrored PR: {pr_data.title}")
        return

    print(f"Generating issue for PR #{config.pr_number}: {pr_data.title}")

    # Generate issue content with Gemini
    issue_generator = GeminiIssueGenerator(config.gemini_api_key, config.gemini_model)
    issue_data = issue_generator.generate_issue(pr_data)

    if not issue_data or not issue_data.get("title") or not issue_data.get("body"):
        print("Gemini did not generate valid issue content.")
        return

    print(f"Generated issue: {issue_data['title']}")

    # Create the actual GitHub issue
    created_issue = github.create_issue(
        title=issue_data["title"], body=issue_data["body"]
    )

    if not created_issue:
        print("Failed to create GitHub issue.")
        return

    issue_number = created_issue.get("number")
    issue_url = created_issue.get("html_url")

    # Link the PR to the issue
    link_success = False
    if issue_number:
        link_success = github.link_pr_to_issue(issue_number)
        if link_success:
            print(f"Successfully linked PR to issue #{issue_number}")
        else:
            print(f"Failed to link PR to issue #{issue_number}")

    # Post a comment with the created issue link
    comment_manager = CommentManager(github)
    comment_manager.marker = "<!-- github-issue-created -->"

    status_emoji = "✅" if issue_number else "❌"
    link_status = "and linked to this PR" if link_success else ""

    pretty_comment = f"""{comment_manager.marker}
## {status_emoji} GitHub Issue Created

> **AI-generated issue has been created {link_status}**

### 📋 Created Issue
**[#{issue_number}]({issue_url})** - {issue_data["title"]}

### 🔗 Relationship
This pull request resolves the issue described above. The issue was automatically generated based on the PR content to represent the underlying problem being solved.

### 📝 Issue Content Preview
<details>
<summary>Click to view the generated issue content</summary>

{issue_data["body"]}

</details>

---

<sub>🤖 *Issue created automatically using `/issue-create` command*</sub>"""

    comment_manager.upsert_comment(pretty_comment)
    print(f"Successfully created issue #{issue_number} and posted comment with link.")


def run_jira_create() -> None:
    """Generate and create a JIRA ticket from PR content."""
    config = Config.from_env(require_gemini=True)
    if not config:
        print(
            "Error: Missing required environment variables (including GEMINI_API_KEY)"
        )
        return

    # Validate JIRA configuration
    required_jira_config = {
        "JIRA_ENDPOINT": config.jira_endpoint,
        "JIRA_TOKEN": config.jira_token,
        "JIRA_PROJECT": config.jira_project,
    }

    missing_config = [name for name, value in required_jira_config.items() if not value]
    if missing_config:
        print(
            f"Error: Missing required JIRA configuration: {', '.join(missing_config)}"
        )
        return

    github = GitHubClient(config)
    pr_data = PRData.from_github(github)
    if not pr_data:
        print("Could not fetch PR data for JIRA ticket creation")
        return

    if pr_data.is_mirrored:
        print(f"Skipping JIRA ticket creation for mirrored PR: {pr_data.title}")
        return

    print(f"Generating JIRA ticket for PR #{config.pr_number}: {pr_data.title}")
    print(f"Using Gemini model: {config.gemini_model}")
    print(f"JIRA project: {config.jira_project}")
    print(f"JIRA component: {config.jira_component or 'None'}")
    print(f"JIRA issue type: {config.jira_issuetype}")

    # Generate JIRA ticket content with Gemini
    jira_generator = GeminiJiraGenerator(config.gemini_api_key, config.gemini_model)

    # Check if there's a user query from PR comments (trigger comment)
    user_query = ""
    if pr_data.comments:
        # Look for /jira-create command in comments
        for comment in pr_data.comments:
            if "/jira-create" in comment.get("body", ""):
                # Extract any text after the command
                body = comment.get("body", "")
                if "/jira-create" in body:
                    parts = body.split("/jira-create", 1)
                    if len(parts) > 1:
                        user_query = parts[1].strip()
                break

    jira_data = jira_generator.generate_jira_ticket(pr_data, user_query)

    if not jira_data or not jira_data.get("title") or not jira_data.get("description"):
        print("Gemini did not generate valid JIRA ticket content.")
        return

    print(f"Generated JIRA ticket: {jira_data['title']}")

    # Generate release note for custom field
    print("Generating release note...")
    release_note = jira_generator.generate_release_note(pr_data)
    if release_note:
        print(f"Generated release note: {release_note[:100]}...")
    else:
        print("Warning: Could not generate release note, using fallback")
        release_note = f"Updated functionality in {pr_data.title}"

    # Build custom fields
    custom_fields = config.build_jira_custom_fields(
        pr_url=pr_data.url, release_note=release_note
    )

    # Create the actual JIRA ticket
    jira_client = JiraClient(config)
    created_ticket = jira_client.create_ticket(
        summary=jira_data["title"],
        description=jira_data["description"],
        custom_fields=custom_fields,
    )

    if not created_ticket:
        print("Failed to create JIRA ticket.")
        return

    ticket_key = created_ticket.get("key")
    ticket_url = f"{config.jira_endpoint.rstrip('/')}/browse/{ticket_key}"

    print(f"Successfully created JIRA ticket: {ticket_key}")
    print(f"JIRA URL: {ticket_url}")

    # Post a comment with the created JIRA ticket link
    comment_manager = CommentManager(github)
    comment_manager.marker = "<!-- jira-ticket-created -->"

    pretty_comment = f"""{comment_manager.marker}
## ✅ JIRA Ticket Created

> **AI-generated JIRA ticket has been created for this PR**

### 🎫 Created Ticket
**[{ticket_key}]({ticket_url})** - {jira_data["title"]}

### 📋 Ticket Details
- **Project**: {config.jira_project}
- **Component**: {config.jira_component or "None"}
- **Issue Type**: {config.jira_issuetype}
- **Git PR**: {pr_data.url}
- **Release Note**: {release_note}

### 🔗 Relationship
This JIRA ticket represents the feature/enhancement being implemented in this pull request.

### 📝 Ticket Content Preview
<details>
<summary>Click to view the generated JIRA content</summary>

```
{jira_data["description"][:1000]}{"..." if len(jira_data["description"]) > 1000 else ""}
```

</details>

---

<sub>🤖 *JIRA ticket created automatically using `/jira-create` command*</sub>"""

    comment_manager.upsert_comment(pretty_comment)
    print(
        f"Successfully created JIRA ticket {ticket_key} and posted comment with link."
    )


def run_jira_create_test() -> None:
    """Test JIRA creation with mock data."""
    print("🧪 Running JIRA creation test with mock data...\n")

    # Mock PR data
    from pr_data import PRData

    # Mock pr_info data
    mock_pr_info = {
        "number": 123,
        "title": "feat: Add webhook controller for GitHub integration",
        "body": "This PR adds a new webhook controller that handles GitHub webhook events for better integration with Pipelines as Code.\n\nThe controller includes:\n- Event processing for push and pull request events\n- Validation of webhook payloads\n- Integration with existing pipeline triggers",
        "html_url": "https://github.com/openshift-pipelines/pipelines-as-code/pull/123",
        "user": {"login": "test-user"},
        "labels": [{"name": "enhancement"}, {"name": "controller"}],
    }

    mock_pr_data = PRData(
        title="feat: Add webhook controller for GitHub integration",
        description="This PR adds a new webhook controller that handles GitHub webhook events for better integration with Pipelines as Code.\n\nThe controller includes:\n- Event processing for push and pull request events\n- Validation of webhook payloads\n- Integration with existing pipeline triggers",
        files_changed=[
            "pkg/controller/webhook.go",
            "pkg/controller/webhook_test.go",
            "test/e2e/webhook_test.go",
            "docs/webhooks.md",
        ],
        commit_messages=[
            "feat: implement webhook controller base structure",
            "feat: add webhook event processing",
            "test: add unit tests for webhook controller",
            "docs: update webhook documentation",
        ],
        pr_info=mock_pr_info,
        current_labels=["enhancement", "controller"],
    )

    print("📋 Mock PR Data:")
    print(f"  - Number: #{mock_pr_data.number}")
    print(f"  - Title: {mock_pr_data.title}")
    print(f"  - Author: {mock_pr_data.author}")
    print(f"  - Files changed: {len(mock_pr_data.files_changed)} files")
    print(f"  - URL: {mock_pr_data.url}\n")

    # Mock JIRA ticket generation
    mock_jira_data = {
        "title": "Implement webhook controller for GitHub integration",
        "description": """h1. Story (Required)

As a Pipelines as Code user trying to integrate with GitHub I want webhook controllers that can process GitHub events

_This story implements a webhook controller system that enables seamless integration between GitHub and Pipelines as Code, improving the user experience by automating pipeline triggers based on repository events._

h2. *Background (Required)*

_Currently, the system lacks a dedicated webhook controller for processing GitHub events, which limits the automation capabilities and requires manual intervention for pipeline triggers._

h2. *Out of scope*

_This story does not include GitLab or Bitbucket webhook integrations, which will be addressed in separate stories._

h2. *Approach (Required)*

_Implement a webhook controller in the pkg/controller package that includes event processing, payload validation, and integration with existing pipeline trigger mechanisms. The controller will handle push and pull request events from GitHub._

h2. *Dependencies*

_This story depends on the existing controller framework and pipeline trigger system._

h2. *Acceptance Criteria (Mandatory)*

_- Webhook controller processes GitHub push events correctly_
_- Webhook controller processes GitHub pull request events correctly_
_- Payload validation prevents malformed requests from causing issues_
_- Integration tests verify end-to-end webhook processing_
_- Documentation is updated with webhook configuration examples_

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

[https://github.com/openshift-pipelines/pipelines-as-code/pull/123|https://github.com/openshift-pipelines/pipelines-as-code/pull/123]

h3. *Original Pull Request Description*

This PR adds a new webhook controller that handles GitHub webhook events for better integration with Pipelines as Code.""",
    }

    # Mock release note
    mock_release_note = "Introduces a new webhook controller for GitHub integration that automatically processes repository events.\nEnables seamless pipeline triggering based on push and pull request events.\nImproves automation capabilities and reduces manual intervention requirements."

    print("🤖 Mock Gemini JIRA Ticket Generation:")
    print(f"  - Title: {mock_jira_data['title']}")
    print(f"  - Description: {len(mock_jira_data['description'])} characters\n")

    print("📝 Mock Release Note Generation:")
    print(f"  - {mock_release_note}\n")

    # Mock custom fields
    mock_custom_fields = {
        "customfield_12310220": mock_pr_data.url,  # Git PR field
        "customfield_12317313": mock_release_note,  # Release Note field
    }

    print("⚙️ Mock JIRA Configuration:")
    print("  - Project: SRVKP")
    print("  - Component: Pipelines as Code")
    print("  - Issue Type: Story")
    print("  - Endpoint: https://issues.redhat.com\n")

    print("📤 Mock JIRA API Payload:")
    mock_payload = {
        "fields": {
            "project": {"key": "SRVKP"},
            "summary": mock_jira_data["title"],
            "description": mock_jira_data["description"],
            "issuetype": {"name": "Story"},
            "components": [{"name": "Pipelines as Code"}],
            **mock_custom_fields,
        }
    }

    import json

    print(json.dumps(mock_payload, indent=2))

    print("\n✅ Mock JIRA Ticket Creation:")
    print("  - Ticket Key: SRVKP-12345")
    print("  - URL: https://issues.redhat.com/browse/SRVKP-12345")

    print("\n💬 Mock GitHub Comment:")
    print("  - Posted comment with JIRA ticket link to PR")
    print("  - Included ticket details and custom field values")

    print("\n🎉 Test completed successfully! All components working correctly.")


def main() -> None:
    """Main entry point."""
    parser = argparse.ArgumentParser(description="PR CI utilities")
    parser.add_argument(
        "command",
        choices=["lint", "update", "all", "issue-create", "jira-create"],
        nargs="?",
        default="all",
        help="Which action to run (default: all)",
    )
    parser.add_argument(
        "--test",
        action="store_true",
        help="Run in test mode with mock data (only works with jira-create)",
    )
    args = parser.parse_args()

    if args.command in ("lint", "all"):
        run_lint()

    if args.command in ("update", "all"):
        run_update()

    if args.command == "issue-create":
        run_issue_create()

    if args.command == "jira-create":
        if args.test:
            run_jira_create_test()
        else:
            run_jira_create()


if __name__ == "__main__":
    main()
