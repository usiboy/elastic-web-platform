#!/usr/bin/env python3
"""只初始化本轮新平台 ECS 试点目录与短期凭据，不改旧入口或旧数据库。"""

import grp
import os
import secrets
import socket
import stat
from pathlib import Path
from urllib.parse import quote


ROOT = Path('/data/ewp-preview-20261006')
PORTS = (15434, 18097, 18443)


def check() -> None:
    if os.geteuid() != 0:
        raise RuntimeError('此工具仅允许 ECS 受控操作员运行')
    if not os.path.ismount('/data') or ROOT.exists() or ROOT.is_symlink():
        raise RuntimeError('数据盘未挂载或本轮目录已存在；不覆盖任何现有工作')
    available = os.statvfs('/data').f_bavail * os.statvfs('/data').f_frsize
    if available < 10 * 1024**3:
        raise RuntimeError('数据盘可用空间低于 10 GiB')
    for port in PORTS:
        with socket.socket() as sock:
            if sock.connect_ex(('127.0.0.1', port)) == 0:
                raise RuntimeError(f'本轮端口 {port} 已被占用')


def private_file(path: Path, content: str) -> None:
    flags = os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, 'O_NOFOLLOW', 0)
    descriptor = os.open(path, flags, 0o600)
    try:
        os.write(descriptor, content.encode('utf-8'))
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def prepare() -> None:
    check()
    nginx_group = grp.getgrnam('nginx').gr_gid
    ROOT.mkdir(mode=0o700)
    for name in ('private', 'state', 'artifacts', 'sources', 'bin'):
        (ROOT / name).mkdir(mode=0o700)
    (ROOT / 'console-web').mkdir(mode=0o750)
    os.chown(ROOT / 'console-web', 0, nginx_group)
    os.chown(ROOT, 0, nginx_group)
    os.chmod(ROOT, 0o750)

    password = secrets.token_urlsafe(32)
    bootstrap = secrets.token_urlsafe(32)
    private = ROOT / 'private'
    private_file(private / 'db-password', password + '\n')
    private_file(private / 'bootstrap.token', bootstrap + '\n')
    database_url = (
        f'postgres://postgres:{quote(password, safe="")}@127.0.0.1:15434/postgres?sslmode=disable'
    )
    common = f'DATABASE_URL={database_url}\n'
    private_file(
        private / 'api.env',
        common
        + f'BOOTSTRAP_TOKEN={bootstrap}\n'
        + 'API_LISTEN_ADDR=127.0.0.1:18097\n'
        + f'SOURCE_STORE_DIR={ROOT / "sources"}\n'
        + f'ARTIFACT_STORE_DIR={ROOT / "artifacts"}\n'
        + 'SITE_PREVIEW_SUFFIX=preview.ecp.lingtong.com\n'
        + 'SITE_PRODUCTION_SUFFIX=sites.ecp.lingtong.com\n'
        + f'TLS_CERT_FILE={private / "server.crt"}\n'
        + f'TLS_KEY_FILE={private / "server.key"}\n',
    )
    private_file(
        private / 'site.env',
        common
        + 'SITE_LISTEN_ADDR=127.0.0.1:18443\n'
        + 'CONTROL_ORIGIN=https://console.ecp.lingtong.com\n'
        + f'ARTIFACT_STORE_DIR={ROOT / "artifacts"}\n'
        + f'TLS_CERT_FILE={private / "server.crt"}\n'
        + f'TLS_KEY_FILE={private / "server.key"}\n',
    )
    if stat.S_IMODE(private.stat().st_mode) != 0o700:
        raise RuntimeError('私有凭据目录权限不正确')
    print('ewp_preview_private_dirs_ready', 'root', ROOT, 'legacyServicesModified', False)


if __name__ == '__main__':
    prepare()
