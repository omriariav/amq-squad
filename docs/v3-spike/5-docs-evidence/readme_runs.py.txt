"""Run every amq-squad command in README.md the way scripts/check-skill-invocations.py
runs the skills: a throwaway HOME and XDG_CONFIG_HOME, no AM_*/AMQ_*/GH_* variables,
the gate's refusing gh stub first on PATH (gh --version answered), one fresh git
fixture per command. Exit 1 (usage) or a stub hit / timeout / panic fails.

usage: python3 -I readme_runs.py <repo> <amq-squad binary>
"""
import importlib.util
import re
import shutil
import sys
import tempfile
from pathlib import Path

repo = Path(sys.argv[1]).resolve()
binary = str(Path(sys.argv[2]).resolve())
spec = importlib.util.spec_from_file_location("gate", repo / "scripts" / "check-skill-invocations.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

readme = repo / "README.md"
text = readme.read_text(encoding="utf-8")
fence = gate.extract_from_fences(text, readme)
inline, skipped = gate.extract_inline(text, readme)
seen = {c for (_, _, c) in fence}
invocations = fence + [i for i in inline if i[2] not in seen]
kind, action = gate.catalog_kind_action(binary)

failures = 0
with tempfile.TemporaryDirectory(prefix="readme-inv-") as tmp:
    root = Path(tmp).resolve()
    home = root / "home"
    home.mkdir()
    env = gate.clean_env(home)
    for index, (path, line, command) in enumerate(invocations):
        fixture = gate.make_fixture(root, index)
        concrete = gate.substitute(command, fixture, kind, action)
        code, detail = gate.run_invocation(binary, concrete, fixture, env)
        bad = code in (1, -1)
        failures += bad
        detail = re.sub(re.escape(str(root)), "<tmp>", detail)[:160]
        print(f"{'FAIL' if bad else 'ok  '} README.md:{line} exit {code}: {command}")
        print(f"       {detail}")
        shutil.rmtree(fixture, ignore_errors=True)
print()
print(f"{len(invocations)} README invocation(s), {failures} failure(s)")
if skipped:
    print(f"{len(skipped)} flagless inline reference(s) not executed:")
    for _, line, span in skipped:
        print(f"    README.md:{line}: {span}")
sys.exit(1 if failures else 0)
