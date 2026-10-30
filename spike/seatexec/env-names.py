"""Print environment variable NAMES (never values) of a live process, via
KERN_PROCARGS2. Flags names matching *_API_KEY, *_TOKEN, *_SECRET (and the
broader KEY|TOKEN|SECRET|PASS|CRED pattern).
Usage: python3 env-names.py PID
"""
import ctypes, ctypes.util, re, struct, sys

libc = ctypes.CDLL(ctypes.util.find_library("c"))
pid = int(sys.argv[1])
mib = (ctypes.c_int * 3)(1, 49, pid)  # CTL_KERN, KERN_PROCARGS2
size = ctypes.c_size_t(1 << 20)
buf = ctypes.create_string_buffer(size.value)
if libc.sysctl(mib, 3, buf, ctypes.byref(size), None, 0) != 0:
    raise SystemExit("sysctl failed")
raw = buf.raw[: size.value]
argc = struct.unpack("i", raw[:4])[0]
parts = raw[4:].split(b"\0", 1)[1].lstrip(b"\0").split(b"\0")
env = []
for p in parts[argc:]:
    if not p:  # an empty string ends envp; Apple's applev strings follow
        break
    env.append(p)
names = sorted({p.split(b"=", 1)[0].decode(errors="replace") for p in env})
strict = re.compile(r"(_API_KEY|_TOKEN|_SECRET)$")
broad = re.compile(r"KEY|TOKEN|SECRET|PASS|CRED", re.I)
for n in names:
    flag = " FLAG" if strict.search(n) else (" broad-match" if broad.search(n) else "")
    print(n + flag)
print(f"# {len(names)} names; strict flags: {sum(1 for n in names if strict.search(n))}")
