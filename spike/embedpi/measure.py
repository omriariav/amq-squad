#!/usr/bin/env python3
"""S3 measurements for one built candidate: python3 measure.py node|bun|upstream.

Runs every pi invocation in the stripped environment the criterion requires
(env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm, plus pi config variables).
Cold runs delete this candidate's own runtime directory under
~/.amq-squad/runtime/ first; nothing else there is touched.
Writes .work/measure-<candidate>.json and prints a summary.
"""
import http.server
import json
import re
import threading
import shutil
import statistics
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

root = Path(__file__).resolve().parent
cand = sys.argv[1] if len(sys.argv) > 1 else 'node'
binary = root / 'bin' / f'embedpi-{cand}'
m = json.loads((root / '.work' / f'manifest-{cand}.json').read_text())
home = Path.home()
runtime = home / '.amq-squad' / 'runtime' / f"{m['version']}-{m['sha256'][:12]}"
exe = runtime / m['executable']
spike = home / '.amq-squad' / 'spike' / 's3'
cwd = spike / 'cwd'
cwd.mkdir(parents=True, exist_ok=True)
agent = spike / 'agent'
agent.mkdir(parents=True, exist_ok=True)
for f in ('models.json', 'settings.json'):
    shutil.copyfile(root / 'agent' / f, agent / f)

ENV = {'HOME': str(home), 'PATH': '/usr/bin:/bin', 'TERM': 'xterm',
       'PI_CODING_AGENT_DIR': str(agent), 'PI_OFFLINE': '1',
       'PI_SKIP_VERSION_CHECK': '1', 'PI_TELEMETRY': '0', 'EMBEDPI_TIMING': '1'}
PROMPT = 'What is 17 plus 25? Reply with just the number.'
PI_ARGS = ['--provider', 'ollama', '--model', 'qwen2.5:32b', '--no-session',
           '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes',
           '--no-context-files', '--no-mcp', '--no-tools', '--thinking', 'off']
out = {'candidate': cand, 'version': m['version'], 'runtime': str(runtime),
       'binary_bytes': binary.stat().st_size,
       'payload_bytes': (root / '.work' / f'payload-{cand}.tar.gz').stat().st_size,
       'unpacked_bytes': sum(f['size'] for f in m['files']), 'files': len(m['files']),
       'env': ENV, 'pi_args': PI_ARGS, 'prompt': PROMPT}


def sh(argv, extra=None, timeout=300):
    env = dict(ENV, **(extra or {}))
    t0 = time.monotonic()
    p = subprocess.run(argv, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    return p, time.monotonic() - t0


def unpack_ms(stderr):
    g = re.search(r'cache_hit=(\w+) unpack_verify_ms=([\d.]+)', stderr)
    return (g.group(1) == 'true', float(g.group(2))) if g else (None, None)


def cold():
    if runtime.exists():
        shutil.rmtree(runtime)


def version_run(label, extra=None):
    p, wall = sh([str(binary), '--version'], extra)
    hit, ms = unpack_ms(p.stderr)
    r = dict(label=label, exit=p.returncode, stdout=p.stdout.strip(), cache_hit=hit,
             unpack_verify_ms=ms, wall_s=round(wall, 4))
    print(json.dumps(r), flush=True)
    return r


class Stub(http.server.BaseHTTPRequestHandler):
    """OpenAI-compatible stub: isolates pi's own launch-to-request latency from Ollama."""
    arrivals = []

    def log_message(self, *a):
        pass

    def do_POST(self):
        Stub.arrivals.append(time.monotonic())
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        base = {'id': 'stub', 'object': 'chat.completion.chunk', 'created': 0, 'model': 'stub'}
        for choice, extra in (({'index': 0, 'delta': {'role': 'assistant', 'content': '42'}, 'finish_reason': None}, {}),
                              ({'index': 0, 'delta': {}, 'finish_reason': 'stop'},
                               {'usage': {'prompt_tokens': 1, 'completion_tokens': 1, 'total_tokens': 2}})):
            self.wfile.write(b'data: ' + json.dumps(dict(base, choices=[choice], **extra)).encode() + b'\n\n')
        self.wfile.write(b'data: [DONE]\n\n')


def prompt_run(label, args=None, extra=None):
    env = dict(ENV, **(extra or {}))
    t0 = time.monotonic()
    p = subprocess.Popen([str(binary), *(args or PI_ARGS), '--mode', 'json', '-p', PROMPT], cwd=cwd, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    first_token = None
    answer = None
    for line in p.stdout:
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        if first_token is None and ev.get('type') == 'message_update':
            first_token = time.monotonic() - t0
        if ev.get('type') == 'message_end' and ev.get('message', {}).get('role') == 'assistant':
            answer = ''.join(c.get('text', '') for c in ev['message']['content'] if c.get('type') == 'text')
    err = p.stderr.read()
    p.wait()
    total = time.monotonic() - t0
    hit, ms = unpack_ms(err)
    r = dict(label=label, exit=p.returncode, answer=answer, cache_hit=hit, unpack_verify_ms=ms,
             first_token_s=round(first_token, 4) if first_token else None, total_s=round(total, 4),
             stderr=err.strip()[-400:])
    if Stub.arrivals and Stub.arrivals[-1] > t0:
        r['launch_to_request_s'] = round(Stub.arrivals[-1] - t0, 4)
    print(json.dumps(r), flush=True)
    return r


def tool(argv):
    p = subprocess.run(argv, capture_output=True, text=True)
    return dict(argv=argv, exit=p.returncode, out=(p.stdout + p.stderr).strip())


# 0. The stripped environment really has no pi, node, bun or npm.
p, _ = sh(['/bin/sh', '-c', 'for x in pi node bun npm; do command -v "$x"; done; echo "PATH=$PATH"'])
out['stripped_path_lookup'] = p.stdout.strip()
assert p.stdout.strip() == 'PATH=/usr/bin:/bin', p.stdout

# Preload the model so prompt timings measure pi, not Ollama's model load.
req = urllib.request.Request('http://localhost:11434/api/generate', method='POST',
                             data=json.dumps({'model': 'qwen2.5:32b', 'prompt': 'hi', 'stream': False,
                                              'options': {'num_predict': 1}, 'keep_alive': '30m'}).encode())
t0 = time.monotonic()
urllib.request.urlopen(req, timeout=300).read()
out['ollama_preload_s'] = round(time.monotonic() - t0, 3)

# 1. Cold and warm --version.
out['cold_version'] = []
for i in range(3):
    cold()
    out['cold_version'].append(version_run(f'cold-version-{i}'))
out['warm_version'] = [version_run(f'warm-version-{i}') for i in range(5)]
out['warm_full_verify'] = version_run('warm-full-verify', {'EMBEDPI_VERIFY': 'full'})

# 2. Prompts: cold start to first prompt, then warm. Real Ollama qwen2.5:32b.
cold()
out['cold_prompt'] = prompt_run('cold-prompt')
out['warm_prompt'] = [prompt_run(f'warm-prompt-{i}') for i in range(3)]

# 2b. Same pi invocation against a local stub provider, so pi's own startup
# cost is visible apart from a shared, possibly busy Ollama.
srv = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Stub)
threading.Thread(target=srv.serve_forever, daemon=True).start()
stub_agent = spike / 'agent-stub'
stub_agent.mkdir(exist_ok=True)
shutil.copyfile(root / 'agent' / 'settings.json', stub_agent / 'settings.json')
(stub_agent / 'models.json').write_text(json.dumps({'providers': {'stub': {
    'baseUrl': f'http://127.0.0.1:{srv.server_address[1]}/v1', 'api': 'openai-completions',
    'apiKey': 'stub', 'models': [{'id': 'stub', 'contextWindow': 32768, 'maxTokens': 16}]}}}))
stub_args = ['--provider', 'stub', '--model', 'stub'] + PI_ARGS[4:]
stub_env = {'PI_CODING_AGENT_DIR': str(stub_agent)}
cold()
out['cold_stub_prompt'] = prompt_run('cold-stub-prompt', stub_args, stub_env)
out['warm_stub_prompt'] = [prompt_run(f'warm-stub-prompt-{i}', stub_args, stub_env) for i in range(5)]
srv.shutdown()

# 3. Gatekeeper and quarantine evidence on the unpacked executable and the launcher.
out['xattr_unpacked_exe'] = tool(['/usr/bin/xattr', '-l', str(exe)])
out['xattr_runtime_dir'] = tool(['/usr/bin/xattr', '-l', str(runtime)])
out['spctl_unpacked_exe'] = tool(['/usr/sbin/spctl', '--assess', '--type', 'execute', '-vvv', str(exe)])
out['codesign_unpacked_exe'] = tool(['/usr/bin/codesign', '-dvv', str(exe)])
out['xattr_launcher'] = tool(['/usr/bin/xattr', '-l', str(binary)])
out['spctl_launcher'] = tool(['/usr/sbin/spctl', '--assess', '--type', 'execute', '-vvv', str(binary)])
out['codesign_launcher'] = tool(['/usr/bin/codesign', '-dvv', str(binary)])
native = [f['path'] for f in m['files'] if f['path'].endswith(('.node', '.dylib'))]
out['native_files'] = native

med = lambda xs: round(statistics.median(xs), 4)
out['summary'] = dict(
    binary_mb=round(out['binary_bytes'] / 1e6, 2), payload_mb=round(out['payload_bytes'] / 1e6, 2),
    unpacked_mb=round(out['unpacked_bytes'] / 1e6, 2), files=out['files'],
    cold_unpack_ms_median=med([r['unpack_verify_ms'] for r in out['cold_version']]),
    cold_version_wall_s_median=med([r['wall_s'] for r in out['cold_version']]),
    warm_unpack_ms_median=med([r['unpack_verify_ms'] for r in out['warm_version']]),
    warm_version_wall_s_median=med([r['wall_s'] for r in out['warm_version']]),
    warm_full_verify_ms=out['warm_full_verify']['unpack_verify_ms'],
    cold_prompt_first_token_s=out['cold_prompt']['first_token_s'],
    cold_prompt_total_s=out['cold_prompt']['total_s'],
    warm_prompt_first_token_s_median=med([r['first_token_s'] for r in out['warm_prompt']]),
    warm_prompt_total_s_median=med([r['total_s'] for r in out['warm_prompt']]),
    answers=[out['cold_prompt']['answer']] + [r['answer'] for r in out['warm_prompt']],
    cold_stub_launch_to_request_s=out['cold_stub_prompt'].get('launch_to_request_s'),
    cold_stub_total_s=out['cold_stub_prompt']['total_s'],
    warm_stub_launch_to_request_s_median=med([r['launch_to_request_s'] for r in out['warm_stub_prompt']]),
    warm_stub_total_s_median=med([r['total_s'] for r in out['warm_stub_prompt']]),
    stub_answers=[out['cold_stub_prompt']['answer']] + [r['answer'] for r in out['warm_stub_prompt']],
)
(root / '.work' / f'measure-{cand}.json').write_text(json.dumps(out, indent=2) + '\n')
print(json.dumps(out['summary'], indent=2))
for k in ('xattr_unpacked_exe', 'spctl_unpacked_exe', 'codesign_unpacked_exe', 'xattr_launcher',
          'spctl_launcher', 'codesign_launcher'):
    print(k, '->', out[k]['exit'], out[k]['out'].replace('\n', ' | ')[:400])
