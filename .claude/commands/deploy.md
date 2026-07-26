# /deploy — Ion release readiness

Prepare an Ion release from the repository that contains this command. This
command validates and reports; it never commits, tags, pushes, deploys, or
changes remote state.

Argument passed: $ARGUMENTS

## 1. Preflight

1. Resolve the repository root with `git rev-parse --show-toplevel` and run all
   commands from there.
2. Record the branch, current commit, remotes, tags, and complete working-tree
   status.
3. Read `AGENTS.md`, `spec/workflow.kvx`, and the active feature spec before
   changing files.
4. Treat every existing modification and untracked file as user-owned.

## 2. Release hygiene

1. Run `bash scripts/release-hygiene.sh`.
2. Stop on any tracked secret, credential, private infrastructure reference,
   local cache, log, analysis document, unrelated repository artifact, or
   compiled development binary.
3. Confirm generated IDE instructions are current with
   `go run . -root ../.. -check` from `spec/specgen`.
4. Review the full diff and list every file proposed for release.

## 3. Verification

1. Discover the repository's documented build and validation commands.
2. Ask before running tests when the current task or user instructions prohibit
   them.
3. Run only authorized checks, preserving their exact exit status.
4. Never interpret a build, health endpoint, screenshot, or simulated response
   as proof of an unverified end-to-end capability.

## 4. Version and release notes

1. Determine the semantic-version impact from the actual diff.
2. Identify every version declaration that would need to change.
3. Draft concise release notes from verified behavior and disclose any
   unverified or blocked acceptance criteria.
4. Do not create local report or log files unless the user explicitly asks for
   an artifact.

## 5. Handoff

Report the hygiene verdict, authorized checks and results, proposed version,
release-note draft, exact files to stage, and any blockers. End with suggested
commit, tag, and push commands for the user to run. Do not run those commands.
