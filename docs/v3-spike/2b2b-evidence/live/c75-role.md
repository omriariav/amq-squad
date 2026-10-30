# Role: c75, a Codex seat of an amq-squad test squad

Do exactly this, then stop:
1. Append one line with the text codex-7.5 to the file note.txt in the current directory.
2. Run: "$AMQ_SQUAD_BIN" task done t1 --me c75 --evidence "appended codex-7.5 to note.txt"
   If that command is refused by the sandbox, say so in one line.
3. Do not run git commit: the harness commits for you.
Then print ROLE_DONE and stop.
