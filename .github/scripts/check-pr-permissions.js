// Check PR author permissions for pull_request_target events.
// Used by the e2e workflow to gate CI runs on external PRs.
//
// Permission check order:
//   1. Trusted bots (dependabot, renovate)
//   2. Org/team membership
//   3. Repository collaborator (write/admin)
//   4. ok-to-test label (maintainer approval for external contributors)
//
// Security: on non-labeled events the ok-to-test label is removed
// to force re-approval after code changes.
module.exports = async ({ github, context, core }) => {
  if (!context || !context.payload || !context.payload.pull_request) {
    core.setFailed(
      "Invalid GitHub context: missing required pull_request information",
    );
    return;
  }

  const actor = context.payload.pull_request.user.login;
  const repoOwner = context.repo.owner;
  const repoName = context.repo.repo;
  const targetOrg = context.repo.owner;

  core.info(`🔍 Starting permission check for user: @${actor}`);
  core.info(`📋 Repository: ${repoOwner}/${repoName}`);
  core.info(`🏢 Target organization: ${targetOrg}`);

  // Condition 1: Check if the user is a trusted bot.
  const trustedBots = ["dependabot[bot]", "renovate[bot]"];
  core.info(`🤖 Checking if @${actor} is a trusted bot...`);
  core.info(`   Trusted bots list: ${trustedBots.join(", ")}`);

  if (trustedBots.includes(actor)) {
    core.info(
      `✅ Condition met: User @${actor} is a trusted bot. Proceeding.`,
    );
    return;
  }
  core.info(`   ❌ User @${actor} is not a trusted bot.`);

  // Condition 2: Check for public membership in the target organization.
  core.info(`\n👥 Condition 2: Checking organization and team membership...`);
  core.info(
    `User @${actor} is not a trusted bot. Checking for membership in '${targetOrg}'...`,
  );
  try {
    // Optional: check membership in one or more org teams (set TARGET_TEAM_SLUGS as comma-separated slugs in workflow env)
    const teamSlugsEnv = process.env.TARGET_TEAM_SLUGS || "";
    const teamSlugs = teamSlugsEnv
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    core.info(`🔧 TARGET_TEAM_SLUGS environment variable: "${teamSlugsEnv}"`);
    core.info(`📝 Parsed team slugs: [${teamSlugs.join(", ")}]`);

    if (teamSlugs.length > 0) {
      core.info(
        `🔍 Checking team membership for ${teamSlugs.length} team(s)...`,
      );
      for (const team_slug of teamSlugs) {
        core.info(`   Checking team: ${team_slug}...`);
        try {
          const membership =
            await github.rest.teams.getMembershipForUserInOrg({
              org: targetOrg,
              team_slug,
              username: actor,
            });
          core.info(
            `   API response for team '${team_slug}': ${JSON.stringify(membership.data)}`,
          );
          if (
            membership &&
            membership.data &&
            membership.data.state === "active" &&
            (membership.data.role === "maintainer" || membership.data.role === "member")
          ) {
            core.info(
              `✅ Condition met: User @${actor} is a member of team '${team_slug}' in '${targetOrg}'. Proceeding.`,
            );
            return;
          } else {
            core.info(
              `   ⚠️ Team membership found but state is not 'active': ${membership.data.state}`,
            );
          }
        } catch (err) {
          core.info(
            `   ❌ User @${actor} is not a member of team '${team_slug}' (or team not found). Error: ${err.message}`,
          );
        }
      }
      core.info(
        `ⓘ User @${actor} is not a member of any configured teams in '${targetOrg}'. Falling back to org membership checks.`,
      );
    } else {
      core.info(
        `ℹ️ No teams configured in TARGET_TEAM_SLUGS. Skipping team membership checks.`,
      );
    }
    core.info(
      `🏢 Checking organization membership for @${actor} in '${targetOrg}'...`,
    );
    try {
      core.info(`   Attempting checkMembershipForUser API call...`);
      await github.rest.orgs.checkMembershipForUser({
        org: targetOrg,
        username: actor,
      });
      core.info(
        `✅ Condition met: User @${actor} is a member of '${targetOrg}'. Proceeding.`,
      );
      return;
    } catch (err) {
      core.info(`   ❌ Private membership check failed: ${err.message}`);
      core.info(`   Attempting checkPublicMembershipForUser API call...`);
      try {
        await github.rest.orgs.checkPublicMembershipForUser({
          org: targetOrg,
          username: actor,
        });
        core.info(
          `✅ Condition met: User @${actor} is a public member of '${targetOrg}'. Proceeding.`,
        );
        return;
      } catch (publicErr) {
        core.info(
          `   ❌ Public membership check failed: ${publicErr.message}`,
        );
        throw publicErr;
      }
    }
  } catch (error) {
    core.info(
      `ⓘ User @${actor} is not a public member of '${targetOrg}'. Checking repository permissions as a fallback.`,
    );
  }

  // Condition 3: Check for write/admin permission on the repository.
  core.info(
    `\n🔐 Condition 3: Checking repository collaborator permissions...`,
  );
  try {
    core.info(`   Attempting getCollaboratorPermissionLevel API call...`);
    const response = await github.rest.repos.getCollaboratorPermissionLevel({
      owner: repoOwner,
      repo: repoName,
      username: actor,
    });

    const permission = response.data.permission;
    core.info(
      `   User @${actor} has '${permission}' permission on ${repoOwner}/${repoName}`,
    );

    if (permission === "admin" || permission === "write") {
      core.info(
        `✅ Condition met: User @${actor} has '${permission}' repository permission. Proceeding.`,
      );
      return;
    } else {
      core.info(
        `   ❌ Permission '${permission}' is insufficient (requires 'write' or 'admin')`,
      );
    }
  } catch (error) {
    core.info(`   ❌ Collaborator permission check failed: ${error.message}`);
  }

  // Condition 4: Check for ok-to-test label (for external contributors approved by maintainers).
  // Only users with repo write access can add labels, so this is inherently gated.
  core.info(`\n🏷️ Condition 4: Checking for ok-to-test label...`);
  if (
    context.payload.action === "labeled" &&
    context.payload.label &&
    context.payload.label.name === "ok-to-test"
  ) {
    core.info(
      `✅ Condition met: ok-to-test label applied by @${context.actor}. Removing label and proceeding with tests.`,
    );
    try {
      await github.rest.issues.removeLabel({
        owner: repoOwner,
        repo: repoName,
        issue_number: context.payload.pull_request.number,
        name: "ok-to-test",
      });
      core.info(`   ok-to-test label removed successfully.`);
    } catch (err) {
      // 404 is expected when multiple matrix jobs race to remove the same label
      if (err.status !== 404) {
        core.setFailed(`   Failed to remove ok-to-test label: ${err.message}`);
        return;
      }
      core.info(`   Label already removed (likely by another matrix job).`);
    }
    return;
  }

  core.info(`   ❌ No ok-to-test label event detected.`);
  core.setFailed(
    `❌ Permission check failed. User @${actor} did not meet any required conditions (trusted bot, org/team member, repo write access, or ok-to-test label).`,
  );
};
