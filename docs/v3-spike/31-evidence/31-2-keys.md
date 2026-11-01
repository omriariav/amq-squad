# Brief 31-2, PR 1: the keys (add, reset, clear, answer)

Branch `v3-31-2`, from origin/v3 at `3f6c995d` (PR #829's merge: brief 31-1
PR 2, `amq-squad up`, the status additions, the two marks, the screen in
the pi extension). Authority: docs/v3.1-plan.md section 7 and
docs/v3-spike/briefs/brief-31-2-keys.md. This is PR 1 of brief 31-2; the
widen key is PR 2 (it waits for t26's typed widen block).

## 1. Q7, the keychain prompt (answered before code)

**The question.** Can this brief build an owner terminal key in the macOS
Keychain with user presence on every signature (Touch ID or the keychain
password prompt), used by a separate `amq-squad gate answer` process, its
public key registered once by a Buzz-signed gate?

**The answer: no, not inside this brief.** The fallback is taken as
written: the answer key shows the gate, posts it over Buzz and says so;
the terminal door waits for a later brief. Nothing is weakened to fit.

**Why, from the platform and from this repository** (read 2026-11-01 on
macOS 26.6.2, arm64; no keychain was read or written):

1. **The user-presence key needs the data protection keychain or CryptoKit.**
   A key whose every use requires user presence is a `SecAccessControl`
   with `kSecAccessControlUserPresence`, either on a Secure Enclave key
   (`SecKeyCreateRandomKey` with `kSecAttrTokenIDSecureEnclave`) or on a
   keychain item. A persistent key of either kind lives in the data
   protection keychain, which refuses a process without the
   `keychain-access-groups` entitlement: `security error -34018` reads
   "A required entitlement isn't present" (errSecMissingEntitlement). The
   amq-squad binary is a Go binary, ad-hoc signed by the linker with no
   entitlements; an entitlement for a keychain group needs a signed binary
   with a provisioning profile, which this project does not ship.
2. **The entitlement-free path is Swift-only.** CryptoKit's
   `SecureEnclave.P256.Signing.PrivateKey` with an access control can keep
   the key as its own `dataRepresentation` blob without the entitlement,
   but CryptoKit has no C API. Go reaches it only through a Swift helper
   compiled with swiftc. The repository builds no Swift, and the owner's
   Mac is not required to carry the Xcode command line tools.
3. **The shipped binary is built without cgo.** The binary `up` installs
   and the squad runs (R54) is built by scripts/build-pi-runtime.sh with
   `CGO_ENABLED=0` (line 114). A cgo binding to Security.framework would be
   left out of that binary, so the terminal door would exist only in test
   builds: production code kept alive by tests, which section 7.2 forbids.
   scripts/ is outside this brief's declared files, and a cgo-free binding
   (purego) is a new module dependency that is not in go.mod and cannot be
   fetched without the network.
4. **The `security` CLI cannot do it.** `security create-keypair` makes
   rsa, dh, dsa or fee keys in the legacy file keychain, guarded by an
   application ACL (`-A`, `-T`); it has no access-control or Secure
   Enclave option and no sign verb. The legacy ACL's prompt offers "Always
   Allow", after which no prompt is shown, so presence would not be on
   every signature: weakened, refused.
5. **A test can only fake it.** The rows' fake keychain could prove the
   verifier's side, but the user-presence prompt itself, the bar's whole
   point, would be checked by no test and by no build the owner runs.

**The fallback, as built** (rows 8 and 9 in their Q7-no form):

- The answer key reads the gate fresh (`amq-squad gate status --gate ID
  --json`), shows its kind, target and 16-hex typed prefix, and, when its
  question has not reached Buzz, posts it (`amq-squad gate post --gate
  ID --json`). It then says: waiting on Buzz; the terminal door is not
  built (Q7); answer in the owner's Buzz DM thread.
- The gates block shows each open typed gate with "waiting on Buzz".
- A gate that is not open (answered over Buzz, denied, expired) is refused
  by the answer key by its state; no verb runs.
- No terminal answer is written: the key never runs `gate answer`, which
  stays the Buzz verifier.

**What a later brief needs for the terminal door:** a signed amq-squad
build with a keychain-access-groups entitlement (or a Swift helper with
CryptoKit, built and shipped beside the binary), the release script's cgo
switch, a gate kind that registers the terminal public key over Buzz, and
gateverify accepting that key with the door and key id in the store.
