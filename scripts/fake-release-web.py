# Fake GitHub for the installer harnesses: a rate-limited API and the
# releases/latest redirect. No network; binds 127.0.0.1 only.
#
#   python3 -I scripts/fake-release-web.py PORT TAG
#
#   /api403/...                         -> 403 (API rate limit exceeded)
#   /web/<owner>/<repo>/releases/latest -> 302 to .../releases/tag/TAG
#   /web/<owner>/<repo>/releases/tag/*  -> 200
#   anything else (e.g. /webnone/...)   -> 404
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(sys.argv[1])
TAG = sys.argv[2]


class Handler(BaseHTTPRequestHandler):
    def reply(self, code, body=b"", headers=()):
        self.send_response(code)
        for k, v in headers:
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def route(self):
        p = self.path.split("?", 1)[0]
        if p.startswith("/api403/"):
            return self.reply(403, b'{"message":"API rate limit exceeded"}',
                              [("Content-Type", "application/json")])
        if p.startswith("/web/") and p.endswith("/releases/latest"):
            loc = "http://127.0.0.1:%d%s/tag/%s" % (PORT, p[: -len("/latest")], TAG)
            return self.reply(302, b"", [("Location", loc)])
        if p.startswith("/web/") and "/releases/tag/" in p:
            return self.reply(200, b"<html>release</html>", [("Content-Type", "text/html")])
        return self.reply(404, b"not found")

    do_GET = route
    do_HEAD = route

    def log_message(self, *args):
        pass


HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
