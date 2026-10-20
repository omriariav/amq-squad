"""Integration check: python3 stands in for a provider, never launches a seat."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

binary = str(Path(sys.argv[1]).resolve())
with tempfile.TemporaryDirectory(prefix="s4-wrapper-") as temp:
    root = Path(temp).resolve()
    (root / "agents/probe").mkdir(parents=True)
    env = {k: v for k, v in os.environ.items()
           if k not in ("AM_ME", "AM_ROOT")}
    provider = [sys.executable, "-c", '''import json, os, pathlib, sys
print(json.dumps({"pid":os.getpid(), "args":sys.argv[1:],
 "seat":os.environ["AMQ_SQUAD_SEAT"], "root":os.environ["AMQ_SQUAD_ROOT"],
 "role":pathlib.Path(os.environ["AMQ_SQUAD_ROLE_PATH"]).read_text()}))''',
                "--model", "literal model", "-c", "model_reasoning_effort=medium"]
    for delimiter in ([], ["--"]):
        proc = subprocess.Popen([binary, "--seat", "probe", "--root", str(root),
                                 *delimiter, *provider], env=env,
                                stdout=subprocess.PIPE, text=True)
        output, _ = proc.communicate(timeout=10)
        assert proc.returncode == 0
        observed = json.loads(output)
        assert observed["pid"] == proc.pid, "wrapper must exec in place"
        assert observed["args"] == provider[3:]
        assert observed["seat"] == "probe" and observed["root"] == str(root)
        assert "S4_ROLE_VISIBLE" in observed["role"]
        record = json.loads((root / "agents/probe/extensions/io.github.omriariav.amq-squad/launch.json").read_text())
        assert record["provider_argv"] == provider
        assert record["runtime"] == Path(sys.executable).name
    inherited = dict(env, AM_ME="probe", AM_ROOT=str(root))
    subprocess.run([binary, "--", *provider], env=inherited,
                   check=True, stdout=subprocess.DEVNULL, timeout=10)
    for args in (["--seat", "../escape"], ["--seat", "other"]):
        result = subprocess.run([binary, *args, "--", *provider], env=inherited,
                                capture_output=True, timeout=10)
        assert result.returncode != 0
print("PASS: argv, exec PID, role, environment, both argv forms, inherited identity, invalid/mismatched seat")
