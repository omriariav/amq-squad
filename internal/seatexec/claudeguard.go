package seatexec

import ()

// SeatDeniedDialogTools are the Claude Code tools that open a dialog in the
// seat's pane. The guard denies both in a seat (internal/cli hook_guard.go):
// a seat never answers a dialog, and a seat's question to anyone is AMQ mail
// to the lead. An AMQ wake doorbell's Enter could otherwise answer one.
var SeatDeniedDialogTools = []string{"AskUserQuestion", "ExitPlanMode"}
