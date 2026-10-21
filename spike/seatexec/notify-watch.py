"""Passive notify observer (closure run). Scans the process table via libproc
roughly every 2 ms and logs each new process whose executable is the AMQ
binary running `__codex-notify`, or the owner's SkyComputerUseClient.
It reads argv only (never the environment block) and never invokes or alters
the owner's notify command.
Usage: python3 notify-watch.py LOGFILE   (stop with kill)
"""
import ctypes, ctypes.util, struct, sys, time, datetime

lib = ctypes.CDLL(ctypes.util.find_library("proc"))
libc = ctypes.CDLL(ctypes.util.find_library("c"))
CTL_KERN, KERN_PROCARGS2 = 1, 49


def pids():
    n = lib.proc_listallpids(None, 0)
    buf = (ctypes.c_int * (n + 64))()
    n = lib.proc_listallpids(buf, ctypes.sizeof(buf))
    return buf[:n]


def path(pid):
    b = ctypes.create_string_buffer(4096)
    return b.value.decode(errors="replace") if lib.proc_pidpath(pid, b, 4096) > 0 else ""


def argv(pid):
    mib = (ctypes.c_int * 3)(CTL_KERN, KERN_PROCARGS2, pid)
    size = ctypes.c_size_t(1 << 18)
    b = ctypes.create_string_buffer(size.value)
    if libc.sysctl(mib, 3, b, ctypes.byref(size), None, 0) != 0:
        return []
    raw = b.raw[: size.value]
    argc = struct.unpack("i", raw[:4])[0]
    rest = raw[4:].split(b"\0", 1)[1].lstrip(b"\0")  # skip exec path
    return [x.decode(errors="replace") for x in rest.split(b"\0")[:argc]]  # argv only, env excluded


def ppid(pid):
    # PROC_PIDTBSDINFO = 3; pbi_ppid at offset 16 of proc_bsdinfo
    b = ctypes.create_string_buffer(136)
    if lib.proc_pidinfo(pid, 3, 0, b, 136) <= 0:
        return -1
    return struct.unpack_from("I", b.raw, 16)[0]


seen = set()
with open(sys.argv[1], "a", buffering=1) as log:
    while True:
        for pid in pids():
            if pid in seen or pid <= 0:
                continue
            p = path(pid)
            if p.endswith("/amq") or p.endswith("SkyComputerUseClient"):
                a = argv(pid)
                if p.endswith("SkyComputerUseClient") or (len(a) > 1 and a[1] == "__codex-notify"):
                    seen.add(pid)
                    ts = datetime.datetime.now().isoformat(timespec="milliseconds")
                    log.write(f"{ts} pid={pid} ppid={ppid(pid)} exe={p} argv={a}\n")
        time.sleep(0.002)
