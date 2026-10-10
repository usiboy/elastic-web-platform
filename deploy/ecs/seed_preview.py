#!/usr/bin/env python3
"""仅在本轮隔离控制 API 为 testA/testB 建独立 Team/Project，凭据不输出。"""

import argparse
import http.client
import json
import os
import socket
import ssl
from pathlib import Path


ROOT = Path('/data/ewp-preview-20261006')
HOST = 'console.ecp.lingtong.com'
PORT = 18097
TESTS = (
    ('a', 'git-preview-a', 'test-a', 'Test A (Vue)'),
    ('b', 'git-preview-b', 'test-b', 'Test B (React)'),
)


class LocalTLS(http.client.HTTPSConnection):
    def connect(self):
        raw = socket.create_connection(('127.0.0.1', PORT), timeout=self.timeout)
        self.sock = self._context.wrap_socket(raw, server_hostname=self.host)


def request(method, path, payload=None, token=None, key=None):
    context = ssl.create_default_context(cafile=str(ROOT / 'private/ca.crt'))
    connection = LocalTLS(HOST, PORT, context=context, timeout=8)
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if key:
        headers['Idempotency-Key'] = key
    if payload is not None:
        headers['Content-Type'] = 'application/json'
        body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    else:
        body = None
    try:
        connection.request(method, path, body=body, headers=headers)
        result = connection.getresponse()
        raw = result.read(64 * 1024 + 1)
        if len(raw) > 64 * 1024:
            raise RuntimeError('control response exceeds limit')
        message = json.loads(raw)
        if result.status not in (200, 201):
            code = message.get('error', {}).get('code', 'unknown')
            raise RuntimeError(f'control HTTP {result.status}: {code}')
        return result.status, message
    finally:
        connection.close()


def private_file(path, content):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    try:
        os.write(fd, content.encode('utf-8'))
        os.fsync(fd)
    finally:
        os.close(fd)


def check():
    if os.geteuid() != 0:
        raise RuntimeError('需要在本轮 ECS 操作员身份运行')
    if not ROOT.is_dir():
        raise RuntimeError('本轮目录不存在')
    bootstrap_path = ROOT / 'private/bootstrap.token'
    if bootstrap_path.stat().st_mode & 0o077:
        raise RuntimeError('引导凭据文件权限过宽')
    _, health = request('GET', '/readyz')
    if health.get('status') != 'ready':
        raise RuntimeError('独立控制 API 未就绪')
    print('ewp_preview_control_ready', 'tlsVerified', True)


def apply():
    check()
    bootstrap = (ROOT / 'private/bootstrap.token').read_text().strip()
    summaries = {}
    for letter, slug, project_slug, project_name in TESTS:
        key = 'ewp-preview-20261006-' + letter
        status, team = request('POST', '/v1/teams', {'slug': slug, 'name': slug}, bootstrap, key + '-team')
        team_id = team['id']
        token_file = ROOT / f'private/owner-{letter}.token'
        if status == 201:
            secret = team.get('ownerToken', '')
            if not secret:
                raise RuntimeError('new Team did not provide one-time Owner identity')
            private_file(token_file, secret + '\n')
        if not token_file.is_file() or token_file.stat().st_mode & 0o077:
            raise RuntimeError('Owner 凭据未以私有文件安全保存')
        owner = token_file.read_text().strip()
        _, project = request(
            'POST', f'/v1/teams/{team_id}/projects',
            {'slug': project_slug, 'name': project_name}, owner, key + '-project',
        )
        summaries[letter] = {'teamId': team_id, 'projectId': project['id'], 'projectStatus': project['status']}
        print('fixture_scope_created', letter, 'teamId', team_id, 'projectId', project['id'], 'status', project['status'])

    summary_path = ROOT / 'state/fixture-projects.json'
    text = json.dumps(summaries, ensure_ascii=False, sort_keys=True) + '\n'
    if summary_path.exists():
        if summary_path.read_text() != text:
            raise RuntimeError('现有 fixture 映射与本次返回不符，拒绝覆盖')
    else:
        private_file(summary_path, text)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument('--dry-run', action='store_true')
    selection.add_argument('--apply', action='store_true')
    arguments = parser.parse_args()
    if arguments.dry_run:
        check()
    else:
        apply()
