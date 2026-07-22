// Package commandevidence records immutable, task-scoped command executions.
//
// A subject snapshot is verified immediately before execution and again before
// its evidence is accepted, but separate commands remain separate transactions:
// another process can mutate the worktree between those command boundaries, so
// callers must use one command when an operation requires one atomic snapshot.
package commandevidence
