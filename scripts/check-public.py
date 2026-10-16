#!/usr/bin/env python3
"""Scan publishable files without displaying secret values; optionally export them."""
import argparse
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
RULES = {
    'private-key': re.compile(r'-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----'),
    'provider-token': re.compile(r'\b(?:sk-(?:proj-)?[A-Za-z0-9_-]{24,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|AKIA[A-Z0-9]{16})\b'),
    'personal-home-path': re.compile('/' + r'Users/(?!YOUR_USERNAME/|example/)[^/\s\"\']+/|' + '/' + r'home/(?!example/|YOUR_USERNAME/)[^/\s\"\']+/'),
}
ASSIGNMENT = re.compile(r'''(?i)["']?(?:token|api_key|api_secret|password|secret|auth_token)["']?\s*[:=]\s*["']([A-Za-z0-9_+/=.-]{20,})["']''')
PLACEHOLDER = re.compile(r'(?i)(?:replace|placeholder|example|change_|your_|test|fixture|dummy|base64)')
PRIVATE_PARTS = {'.data', '.cache', 'output', 'bin', 'node_modules', 'dist', '.playwright-cli', '.agents', '.codex', '.aws', '__pycache__', 'backups', 'storage'}
PRIVATE_EXTENSIONS = {'.pem', '.key', '.p12', '.pfx', '.jks', '.keystore', '.db', '.dump', '.backup', '.apk', '.exe'}


def run_git(args):
    return subprocess.run(['git', '-C', str(ROOT), *args], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True).stdout


def public_files():
    # Use the real index where present, so accidentally tracked ignored files are
    # inspected too. Otherwise evaluate .gitignore using an isolated empty index.
    try:
        top = Path(run_git(['rev-parse', '--show-toplevel']).decode().strip()).resolve()
    except subprocess.CalledProcessError:
        top = None
    if top == ROOT:
        raw = run_git(['ls-files', '--cached', '--others', '--exclude-standard', '-z'])
    else:
        with tempfile.TemporaryDirectory(prefix='mobile-autorun-ignore-') as tmp:
            subprocess.run(['git', 'init', '-q', tmp], check=True, stdout=subprocess.DEVNULL)
            raw = run_git(['--git-dir=' + str(Path(tmp) / '.git'), '--work-tree=' + str(ROOT),
                           '-c', 'core.excludesFile=/dev/null', 'ls-files', '--others', '--exclude-standard', '-z'])
    return sorted({os.fsdecode(p) for p in raw.split(b'\0') if p})


def local_values():
    # Compare against locally configured credentials and device IDs in memory.
    # This does not transmit or print them, and CI works without private files.
    values = set()
    env = ROOT / '.env'
    if env.is_file():
        for line in env.read_text().splitlines():
            key, sep, value = line.partition('=')
            value = value.strip().strip('\"\'')
            if sep and any(part in key.upper() for part in ('PASSWORD', 'SECRET', 'TOKEN', 'KEY', 'DATABASE_URL')):
                if len(value) >= 8 and not PLACEHOLDER.search(value):
                    values.add(value)
                if key == 'DATABASE_URL':
                    try:
                        password = urlsplit(value).password
                        if password and len(password) >= 8:
                            values.add(unquote(password))
                    except ValueError:
                        pass
    for path in (ROOT / 'configs').glob('*.local.json'):
        for key, value in json.loads(path.read_text()).items():
            if any(word in key.lower() for word in ('token', 'secret', 'password', 'serial', 'key')):
                if isinstance(value, str) and len(value) >= 8 and not PLACEHOLDER.search(value):
                    values.add(value)
    return values


def entropy(value):
    return -sum((value.count(c) / len(value)) * math.log2(value.count(c) / len(value)) for c in set(value))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--export-dir', type=Path, help='new directory outside this project; refuses existing destinations')
    args = parser.parse_args()
    files = public_files()
    secrets = local_values()
    findings = []
    for relative in files:
        path = ROOT / relative
        if not path.exists() and not path.is_symlink():
            continue  # deleted tracked files are absent from the current tree
        parts = Path(relative).parts
        private = (any(part in PRIVATE_PARTS for part in parts)
                   or (path.name.startswith('.env') and path.name != '.env.example')
                   or path.name.endswith('.local.json') or path.suffix in PRIVATE_EXTENSIONS)
        if private or path.is_symlink():
            findings.append((relative, 0, 'private-file-or-symlink'))
            continue
        data = path.read_bytes()
        try:
            content = data.decode('utf-8')
        except UnicodeDecodeError:
            findings.append((relative, 0, 'binary-file-review-required'))
            continue
        for number, line in enumerate(content.splitlines(), 1):
            for name, pattern in RULES.items():
                if pattern.search(line):
                    findings.append((relative, number, name))
            if any(value in line for value in secrets):
                findings.append((relative, number, 'known-local-private-value'))
            for match in ASSIGNMENT.finditer(line):
                value = match.group(1)
                if not PLACEHOLDER.search(value) and entropy(value) >= 3.5:
                    findings.append((relative, number, 'credential-assignment'))
    if findings:
        for path, line, reason in findings:
            print(f'{path}:{line}: {reason}')
        print(f'FAIL: {len(findings)} findings; values suppressed.')
        return 1
    print(f'PASS: {len(files)} publishable files; no matches for configured credential/path rules.')
    print('Scope: current tree only; no Git history, remote services, image OCR or credential rotation.')
    if args.export_dir:
        destination = args.export_dir.resolve()
        if destination == ROOT or ROOT in destination.parents:
            parser.error('export destination must be outside this project')
        destination.mkdir(parents=True, exist_ok=False)
        for relative in files:
            source = ROOT / relative
            if source.is_file():
                target = destination / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
        print(f'Exported clean source: {destination}')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
