#!/usr/bin/env python3
"""Brief 31-1 PR 1: show each deleted Go file is dead on the branch.

Usage (from the repository root): python3 -I deadgrep.py BASE_REF

For every .go file deleted between BASE_REF and HEAD it lists the file's
top-level names (functions, types, vars, consts; methods are covered by
their type) and searches the branch's Go files for each: an exported name
as <pkg>.<Name> anywhere and as a word in its own package's directory, an
unexported name as a word in its own package's directory. It prints each
file with its names and every hit; "callers: none" means dead."""
import re, subprocess, sys, pathlib

base = sys.argv[1]
files = subprocess.run(["git", "diff", "--name-status", base + "...HEAD"], capture_output=True, text=True, check=True).stdout.split("\n")
deleted = sorted(l.split("\t")[1] for l in files if l.startswith("D\t") and l.endswith(".go"))
gofiles = [p for p in pathlib.Path(".").rglob("*.go") if ".worktrees" not in p.parts and "node_modules" not in p.parts]
texts = {str(p): p.read_text(errors="replace") for p in gofiles}
decl = re.compile(r"^(?:func\s+(\w+)|type\s+(\w+)|(?:var|const)\s+(\w+))", re.M)
block = re.compile(r"^(?:var|const)\s*\((.*?)^\)", re.M | re.S)
total = 0
for f in deleted:
    src = subprocess.run(["git", "show", base + ":" + f], capture_output=True, text=True, check=True).stdout
    names = {n for m in decl.finditer(src) for n in m.groups() if n}
    for b in block.finditer(src):
        names |= set(re.findall(r"^\s+(\w+)\b", b.group(1), re.M))
    names -= {"init", "_"}
    pkgdir = str(pathlib.Path(f).parent)
    pkg = pathlib.Path(f).parent.name
    hits = []
    for n in sorted(names):
        word = re.compile(r"\b" + re.escape(n) + r"\b")
        qual = re.compile(r"\b" + re.escape(pkg) + r"\." + re.escape(n) + r"\b")
        for path, text in texts.items():
            same = str(pathlib.Path(path).parent) == pkgdir
            if (same and word.search(text)) or (n[0].isupper() and not f.endswith("_test.go") and qual.search(text)):
                hits.append(f"{n} in {path}")
    total += len(hits)
    print(f"{f}: {', '.join(sorted(names))}")
    print("  callers: " + ("; ".join(hits) if hits else "none"))
sys.exit(1 if total else 0)
