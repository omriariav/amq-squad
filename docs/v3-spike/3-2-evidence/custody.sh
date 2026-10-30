#!/bin/bash
# Credential custody measurement for brief 3-2 (lead ruling 12): what a seat
# can reach on this single-user Mac. Read-only; prints names, counts and
# attributes, never a secret value.
W=/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571
echo "# measured $(date -u +%FT%TZ) as uid $(id -u) on $(sw_vers -productVersion 2>/dev/null)"
echo "## 1. credential-looking variable names in the owner's environment (names only)"
env | cut -d= -f1 | grep -E 'TOKEN|SECRET|PASSWORD|API_KEY|^GH_|^GITHUB|SSH_AUTH_SOCK|SSH_AGENT|NETRC|CREDENTIAL' | sort || echo "(none)"
echo "## 2. gh auth status with the full environment (gh masks the token)"
gh auth status 2>&1 | sed -E 's/(gho_|ghp_|github_pat_)[A-Za-z0-9_]+/\1****/g'
echo "## 3. gh auth status from a scrubbed seat-like environment (HOME and PATH only)"
env -i HOME="$HOME" PATH="$PATH" gh auth status 2>&1 | sed -E 's/(gho_|ghp_|github_pat_)[A-Za-z0-9_]+/\1****/g'
echo "## 4. can a scrubbed process obtain a token? (exit code and length only)"
tok=$(env -i HOME="$HOME" PATH="$PATH" gh auth token 2>/dev/null); echo "gh auth token exit $? length ${#tok}"; unset tok
echo "## 5. the gh keychain item (attributes only, no -w)"
security find-generic-password -s "gh:github.com" 2>&1 | grep -E 'keychain:|"svce"|"acct"|class' | sed -E 's/"acct"<blob>=.*/"acct"<blob>=<redacted>/'
echo "## 6. gh config files (presence; token lines counted, not shown)"
ls -la "$HOME/.config/gh/" 2>&1 | sed 1d
[ -f "$HOME/.config/gh/hosts.yml" ] && echo "oauth_token lines in hosts.yml: $(grep -c oauth_token "$HOME/.config/gh/hosts.yml")"
echo "## 7. SSH agent: launchd socket reachable without SSH_AUTH_SOCK in the environment?"
echo "launchctl getenv SSH_AUTH_SOCK: $(launchctl getenv SSH_AUTH_SOCK 2>&1 || true)"
S=$(launchctl getenv SSH_AUTH_SOCK)
echo "socket mode: $(ls -la "$S" 2>&1 | awk '{print $1}')"
echo "keys listed through the launchd socket by a process with no SSH_AUTH_SOCK of its own: $(env -i HOME="$HOME" PATH="$PATH" SSH_AUTH_SOCK="$S" ssh-add -l 2>&1 | grep -vc 'no identities')"
env -i HOME="$HOME" PATH="$PATH" SSH_AUTH_SOCK="$S" ssh-add -l 2>&1 | awk '{print $1, $NF}'
echo "## 8. SSH key files a same-UID process can read (names and modes)"
ls -la "$HOME/.ssh" 2>&1 | awk 'NR>1 {print $1, $NF}'
echo "## 9. git credential helper (from the worktree's view of global config)"
git -C "$W" config --get-all credential.helper || echo "(none)"
echo "## 10. remote URL scheme of the two S6 repositories (https uses the helper or gh; ssh uses the agent)"
gh repo view omriariav/amq-noc --json sshUrl,url --jq '.url' 2>&1
