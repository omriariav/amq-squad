PreToolUse attachments for pretool.py (Claude's own record):
  {"toolUseID": "toolu_01ESvTh61wRkXQT8QtLJcRNi", "durationMs": 5034, "timedOut": true, "timeoutMs": 5000, "exitCode": null, "stderr": null}
  {"toolUseID": "toolu_013Hwydm3Ngzy3XUQVAV7SZM", "durationMs": 44, "timedOut": null, "timeoutMs": null, "exitCode": 1, "stderr": "Failed with non-blocking status code: s4 fault: exit 1 without JSON deny"}
  {"toolUseID": "toolu_01YByGEoa37pCHg2CvrRNbQU", "durationMs": 15, "timedOut": null, "timeoutMs": null, "exitCode": 127, "stderr": "Failed with non-blocking status code: /bin/sh: /Users/omri/.amq-squad/spike/s4/project/.claude/hooks/pretool.py: No such file or directory"}

| trial | command | tool proceeded | is_error | tool_use->tool_result (s) | PreToolUse->PostToolUse hook (s) | result excerpt |
|---|---|---|---|---|---|---|
| ok | `cat "$AMQ_SQUAD_ROLE_PATH"` | yes | False | 0.88 | 0.65 | # S4 launch probe

Seat: s4-claude
Runtime: claude

Read this role and report S4_ROLE_VISIBLE with your seat handle. Per |
| deny | `ls -1 # s4-fault-deny` | no | True | 0.17 | n/a (hook never ran) | PreToolUse:Bash hook error: S4 control: deny |
| sleep | `ls -1 # s4-fault-sleep` | yes | False | 5.11 | 5.07 | README.md |
| exit1 | `ls -1 # s4-fault-exit1` | yes | False | 0.36 | 0.30 | README.md |
| missing | `ls -1 # s4-fault-missing` | yes | False | 0.44 | n/a (hook never ran) | README.md |
