# Liferay Tunnel Project Rules

These are workspace-specific rules that apply to the development of the Liferay Tunnel project.

## GitHub Issue & PR Linking
1. **Always Link PRs to Issues**: Every Pull Request (PR) must be linked to its corresponding GitHub issue. You must explicitly include `Closes #<issue-id>` or `Fixes #<issue-id>` in the PR description so that GitHub links the PR and branch to the issue and closes it automatically upon squash merge.
2. **Commit References**: All commit messages must reference the issue number suffix (e.g., `feat: access control enhancements (#173)`) to maintain a clean git history and link the commits to the issue.
3. **No Direct Master Pushes**: Direct pushes to the `master` branch are strictly prohibited. All changes must be developed on a branch, verified with unit tests and lint checks, and merged via a Pull Request.
4. **No Admin Bypass of Branch Protection**: Do NOT use `--admin` flags (e.g., in `gh pr merge --admin`) or any other administrative privileges to bypass branch protection rules or CI/CD checks. You must wait for all required status checks to pass and for explicit approval/merge actions from the user. Rules must be followed to the letter without exception.
