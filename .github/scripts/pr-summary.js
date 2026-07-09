// Posts/updates a single sticky PR comment summarizing this CI run's job results.
// actions/github-script has no `script-path` input, so ci.yml's inline `script:`
// requires this file and calls the exported function, passing `github`/`context`.
// JOB_RESULTS/COVERAGE_PCT are read from process.env (set by the calling step).

const MARKER = "<!-- ci-pr-summary -->";

function icon(result) {
  if (result === "success") return "✅";
  if (result === "skipped") return "⏭️";
  if (result === "cancelled") return "🚫";
  return "❌";
}

module.exports = async ({ github, context }) => {
  const jobResults = JSON.parse(process.env.JOB_RESULTS || "{}");
  const coveragePct = process.env.COVERAGE_PCT || "n/a";

  const rows = Object.entries(jobResults)
    .map(([name, info]) => `| ${name} | ${icon(info.result)} ${info.result} |`)
    .join("\n");

  const body = [
    MARKER,
    "## CI Summary",
    "",
    "| Job | Result |",
    "| --- | --- |",
    rows,
    "",
    `**Unit test coverage:** ${coveragePct}`,
  ].join("\n");

  const { data: comments } = await github.rest.issues.listComments({
    owner: context.repo.owner,
    repo: context.repo.repo,
    issue_number: context.issue.number,
  });

  const existing = comments.find((c) => c.body.includes(MARKER));

  if (existing) {
    await github.rest.issues.updateComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      comment_id: existing.id,
      body,
    });
  } else {
    await github.rest.issues.createComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: context.issue.number,
      body,
    });
  }
};
