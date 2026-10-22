# S1 owner run: the real-key gate answer

**Done 2026-10-22: PASS with gate g-owner-02 (S1-report section 12).** The sheet below is kept as the procedure, updated with what the run taught.

**Window: once the gate question appears in the Buzz DM (step 12), you have
30 minutes to answer it.** Everything before step 12 has no time limit.

**Rules for this run:**

- Only public values go on the clipboard: the body npub, the gate id, the
  `approve …` line, and the DM channel id. Nothing else.
- Your nsec never goes on the clipboard, on a command line, into shell
  history, into a file, or into a log.
- The nsec reaches the signer one of two ways:
  - **A**: the password manager's CLI prints it into a pipe (step 4b).
  - **B**: you type it into the hidden prompt (step 8).

  Do not paste it, and do not take it from the phone.

Before you start:

- You answered "yes" to the decision in `S1-report.md` section 8.1: you will
  export your Buzz nsec once to the Mac, by A or B. If your answer is "no",
  stop here.
- Have your npub and relay URL from the spike brief. Relay (owner, 2026-10-22):
  `wss://omriariav.communities.buzz.xyz`, a Buzz communities relay; expect NIP-42
  AUTH on it, which `amq-owner-sign` and the verifier handle. npub (owner, 2026-10-22):
  `npub1yv3jyq50hejhyfer9ff4vjj3revl56dm4mf0mv6wxrzrr7gfdecs8svg9g`. The owner has
  Buzz Desktop on the Mac and will pair the Buzz iOS app later; the gate answer can
  come from either once paired.
- For A: a password manager CLI that prints the nsec, and nothing else, to
  stdout. The command itself must not contain the nsec.

Steps marked **[Mac]** happen in one Terminal window. Steps marked
**[Phone]** happen in the Buzz app. The only thing you bring from the phone to
the Mac is the DM channel id, in step 10.

## Steps

1. **[Mac]** Open a checkout of `v3` in scratch:
   `git -C ~/code/amq-squad worktree add --detach ~/.amq-squad/spike/s1/run v3`
   Expect: `HEAD is now at …`.

2. **[Mac]** Go to the scripts:
   `cd ~/.amq-squad/spike/s1/run/spike/gateverify`
   Expect: no output.

3. **[Mac]** Check that you have the current scripts:
   `grep -q allow-unbound main.go && echo current`
   Expect: `current`. If nothing prints, stop: v3 does not have this round yet.

4. **[Mac]** Set your values, typed from the spike brief:
   `export NPUB='npub1yv3jyq50hejhyfer9ff4vjj3revl56dm4mf0mv6wxrzrr7gfdecs8svg9g' RELAY='wss://omriariav.communities.buzz.xyz'`
   Expect: no output.

4b. **[Mac]** Option A only. Name the command that prints your nsec.
    Owner's setup (2026-10-22): 1Password CLI 2.41 with an Environment named
    "omri" holding `BUZZ_NSEC_FULL` (the whole nsec; `BUZZ_NSEC_FIRST` is not
    used). `op run --environment` wants the Environment ID (`<op-environment-id>`), not the name
    (1Password app: Developer > View Environments > the environment > Manage
    environment > Copy environment ID). `op run` masks secrets on stdout, so
    the pipe needs `--no-masking`:
    `export S1_OWNER_KEY_CMD='op run --environment <op-environment-id> --no-masking -- sh -c "echo \"\$BUZZ_NSEC_FULL\""'`
    (Not `printf %s\n` unquoted inside `sh -c`: the shell strips the backslash
    and `printf` appends a literal `n`, which made the first run fail with
    "not a valid nsec".) Check the shape without showing the key:
    `bash -c "$S1_OWNER_KEY_CMD" | python3 -I ~/.amq-squad/spike/s1/nsec-shape.py`
    (script kept in `spike/gateverify/nsec-shape.py`). Expect `chars=63`,
    `valid_as_is=True`.
    1Password may ask for Touch ID; that is expected.
    `export S1_OWNER_KEY_CMD='<password manager read command>'`
    Expect: no output. History keeps the command, never the nsec. With
    option B, skip this step.

5. **[Mac]** Start the run (presence on, so Buzz can find the body by name):
   `S1_PRESENCE='amq-squad S1 gate' ./realkey-run.sh "$NPUB" "$RELAY" g-owner-01`
   Expect: `owner: npub1…`, then `body:  <64 hex>`, then
   `You are about to sign these grants…` with 7 `kind …` lines.

6. **[Mac]** Compare the `body:` line with the `body pubkey:` line. They must
   match.
   Expect: the prompt `…type its first 8 characters to sign:`.

7. **[Mac]** Type the first 8 characters of the body pubkey. Press Enter.
   Expect: `owner nsec or hex (hidden):`. With option A, the key comes from
   the pipe and the run continues by itself.

8. **[Mac]** Option B only: type your nsec at the hidden prompt, reading it
   from your password manager. Do not paste it. Press Enter. Nothing shows
   while you type.
   Expect: `owner pubkey: <your hex>`, `wrote 7 signed grants`,
   `published kind 30177 policy …`, `== D: OWNER STEP (phone)`, a QR code,
   `body npub: npub1…` (also put on the clipboard: it is public), and the
   script stops. A `policy was not published` line is fine. Step 11 then
   retries the publication: do steps 6 to 8 once more when asked. To skip
   the retry, put `S1_SKIP_POLICY=1` in front of the step 11 command.

9. **[Phone]** In Buzz, start a new DM: search for the agent named
   "amq-squad S1 gate" (it appears within a minute of step 8). A pasted npub
   is not found: the picker lists profiles and agents only.
   Expect: an empty DM with the body.

10. **[Phone]** Open the DM's details and copy its channel id. Send it to the
    Mac by any route you trust (for example, AirDrop to Notes).
    Expect: a channel id string on the Mac.

11. **[Mac]** Run again with the channel id:
    `S1_PRESENCE='amq-squad S1 gate' S1_DM_CHANNEL='<channel id>' ./realkey-run.sh "$NPUB" "$RELAY" g-owner-01`
    Expect: `== E`, `== F`, `== 2: waiting…`, `== 3: gate record stored …`,
    the gate text, `== 4: gate question posted as event …`, then
    `== 5: OWNER (phone)… approve g-owner-01 merge <16 hex>`.
    If it prints `RESULT FAIL … endpoint not authenticated`, stop and send the
    log from step 15.

12. **[Phone]** In the DM, open the message that starts `amq-squad gate
    g-owner-01`.
    Expect: the gate text, ending in the `approve …` and `deny …` lines.

13. **[Phone]** Reply **in that message's thread** (hover the message, "Reply in
    thread"; never the main DM box, a plain DM is rejected as
    `not_in_gate_thread`) with the `approve …` line, exactly as the message shows it. You may copy it from the message itself.
    Expect: your reply in the thread. The body may add a result row.

14. **[Mac]** Wait. No typing is needed.
    Expect, within about a minute: `== 6: answer event …`,
    `{"result":"approve",…}`, `envelope ok: …`, then three lines
    (`already_consumed`, `gate_mismatch`, `wrong_pubkey`), `== timing: …`,
    `RESULT PASS … via="serve ingress ledger" …`, and last `PASS`.

15. **[Mac]** Send the result to the lead:
    `tail -40 ~/.amq-squad/spike/s1/realkey/g-owner-01/run.log`
    Expect: the same lines as step 14. Paste these into the spike thread.
    They hold no secret.

16. **[Mac]** Clean up:
    `./teardown.sh && cd ~ && git -C ~/code/amq-squad worktree remove ~/.amq-squad/spike/s1/run`
    Expect: `removed … and the share files`.

17. **[Phone]** In Buzz, archive the DM with the body.
    Expect: the DM leaves your list. The grants expire in 30 days on their own.

## If something stops

- The script stops with exit 10 or 11. An owner step is pending. Do the step
  it prints, then rerun the same command.
- The final line is `FAIL`. Send step 15's output to the lead. Nothing needs
  undoing beyond step 16.
- The final line is `NOT PROVEN`. Your answer verified, but serve never
  recorded it, so delivery by serve is not proven. Send step 15's output to
  the lead.
- `render or store` fails at step 11. A record for that gate id already
  exists. Rerun step 11 with a new gate id, for example `g-owner-02`.
- You answered `deny` by mistake. The run reports a verified deny and FAIL.
  Rerun step 11 with a new gate id, for example `g-owner-02`.
