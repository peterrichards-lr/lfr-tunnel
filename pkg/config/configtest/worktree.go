package configtest

import (
	"os"
	"path/filepath"
)

// IsNestedWorktreeRoot reports whether dir is the root of a git worktree nested inside the tree
// being walked -- an agent worktree under .claude/worktrees, or anything else `git worktree add`
// put here.
//
// Its contents are a second checkout of THIS repository, so every file in it is a duplicate. A
// gate that walks the FILESYSTEM rather than git's index cannot see the `.git/info/exclude`
// entry that hides those directories, so it reads them as ordinary source. Which way that goes
// wrong depends on what the gate asserts, and both halves have been measured:
//
//   - An absence-style gate reports the copies as offenders. On 2026-09-08 that made `make test`
//     fail on a clean master with three agent worktrees present, flagging the same three
//     legitimate lines nine times and blocking every push through the pre-push hook (#1815). On
//     2026-09-23 the same shape hit TestEveryNotificationSendGoesThroughTheFunnel, which named
//     two copies of the funnel itself as bypasses of the funnel (#2211).
//   - A presence-style gate goes FALSE GREEN, because the duplicate satisfies the search.
//     Measured for #2211: undocumenting -inspector-port while a copy of docs/ sat in a nested
//     worktree left TestEveryFlagIsDocumented passing; removing the nested copy alone turned it
//     red. That is the more dangerous half, because nobody looks at a green gate.
//
// The test is the presence of `.git` as a FILE. A real repository root carries `.git` as a
// directory; a worktree root carries it as a regular file holding a `gitdir:` pointer. That is
// what identifies the class, rather than a directory name -- so this keeps working if the tooling
// stops using `.claude/worktrees`, which a hardcoded path would not.
//
// It lives here, exported, rather than beside any one gate. #1815 fixed two walkers and left
// three, because the rule was COPIED into pkg/server instead of shared and a third walker had
// nowhere to pick it up from (#2211). TestEveryFilesystemWalkingGateSkipsNestedWorktrees derives
// the set of walkers from the source and fails if one of them does not consult this function, so
// a sixth walker is covered by being written rather than by being remembered.
func IsNestedWorktreeRoot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.Mode().IsRegular()
}
