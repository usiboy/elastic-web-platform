"""Local-only, read-only browser acceptance bridge to the actual ECS static bytes/API.

The browser uses disposable placeholder identities; the protected Owner files
are read by this process and transmitted only to the verified control TLS host.
Do not expose the loopback listeners through a reverse proxy or port forward.
"""

import http.client
import json
import os
import socket
import ssl
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


CONTROL_HOST = "console.ecp.lingtong.com"
SITE_HOST = "p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com"
ECS_IP = "114.55.237.62"
SECRETS = {
    "Bearer ewp_qa_a": os.environ["EWP_LIVE_ECS_OWNER_A_FILE"],
    "Bearer ewp_qa_b": os.environ["EWP_LIVE_ECS_OWNER_B_FILE"],
}
TRUST = ssl.create_default_context(cafile=os.environ["EWP_LIVE_ECS_CA_FILE"])


class VerifiedControlConnection(http.client.HTTPSConnection):
    def connect(self):
        plain = socket.create_connection((ECS_IP, 443), timeout=self.timeout)
        self.sock = self._context.wrap_socket(plain, server_hostname=self.host)


class ReadOnlyHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.forward()

    def do_HEAD(self):
        self.forward()

    def do_PUT(self):
        # One known-to-fail test request is permitted to exercise the live UI
        # error state. Every other write is rejected locally before forwarding.
        if (self.server.server_port != 8765
                or self.path != "/v1/teams/t_eee8046ca49c271f5bea06acf285be66/projects/p_e5123bae12388454bb82fc1590750ab9/resources"
                or self.headers.get("Authorization") != "Bearer ewp_qa_b"):
            self.send_error(405)
            return
        size = int(self.headers.get("Content-Length", "0"))
        if size <= 0 or size > 512:
            self.send_error(403)
            return
        body = self.rfile.read(size)
        try:
            expected = {"expectedVersion": 1, "cpuMilli": 1200, "memoryMiB": 512, "storageMiB": 256}
            if json.loads(body) != expected:
                self.send_error(403)
                return
        except (ValueError, UnicodeDecodeError):
            self.send_error(403)
            return
        self.forward(body)

    def forward(self, body=None):
        is_control = self.server.server_port == 8765
        host = CONTROL_HOST if is_control else SITE_HOST
        connection = (VerifiedControlConnection(host, context=TRUST, timeout=10) if is_control
                      else http.client.HTTPConnection(ECS_IP, 80, timeout=10))
        headers = {"Host": host, "Accept": self.headers.get("Accept", "*/*")}
        placeholder = self.headers.get("Authorization", "")
        if is_control and self.path.startswith("/v1/") and placeholder in SECRETS:
            with open(SECRETS[placeholder], encoding="ascii") as token_file:
                headers["Authorization"] = "Bearer " + token_file.read().strip()
        if body is not None:
            headers["Content-Type"] = "application/json"
        try:
            connection.request(self.command, self.path, body=body, headers=headers)
            response = connection.getresponse()
            data = response.read(2 * 1024 * 1024)
            self.send_response(response.status)
            for name, value in response.getheaders():
                if name.lower() not in ("connection", "content-length", "transfer-encoding", "content-encoding"):
                    self.send_header(name, value)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
        except (OSError, ssl.SSLError, http.client.HTTPException):
            self.send_error(502, "control/site upstream unavailable")
        finally:
            connection.close()

    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    for port in (8765, 8766):
        threading.Thread(target=ThreadingHTTPServer(("127.0.0.1", port), ReadOnlyHandler).serve_forever, daemon=True).start()
    threading.Event().wait()
