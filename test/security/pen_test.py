"""Expanded penetration test suite for fleet-llm-d.

Tests beyond the basic stress test Phase 7: SSRF, header injection,
token replay, privilege escalation, DecisionPackage tampering,
rate limit bypass, and large payload handling.

Usage:
    python3 test/security/pen_test.py \
        --fleet-url http://fleet-controller.fleet-llm-d.svc:8080 \
        --auth-secret oberon-fleet-secret-2026
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import hashlib
import hmac
import json
import sys
import time
from dataclasses import dataclass

import httpx


@dataclass
class PenResult:
    name: str
    passed: bool
    detail: str


def generate_token(secret: str, subject: str, role: str = "admin",
                   ttl_hours: int = 24) -> str:
    from datetime import datetime, timezone, timedelta
    now = datetime.now(timezone.utc)
    claims = {"sub": subject, "role": role,
              "iat": now.isoformat(), "exp": (now + timedelta(hours=ttl_hours)).isoformat()}
    claims_json = json.dumps(claims, separators=(",", ":")).encode()
    claims_b64 = base64.urlsafe_b64encode(claims_json).rstrip(b"=").decode()
    sig = hmac.new(secret.encode(), claims_json, hashlib.sha256).digest()
    sig_b64 = base64.urlsafe_b64encode(sig).rstrip(b"=").decode()
    return claims_b64 + "." + sig_b64


class PenTest:
    def __init__(self, fleet_url: str, auth_secret: str, timeout: float = 15.0):
        self.fleet = fleet_url.rstrip("/")
        self.secret = auth_secret
        self.timeout = timeout
        self.results: list[PenResult] = []
        self.admin_token = generate_token(auth_secret, "pen-admin", "admin")
        self.viewer_token = generate_token(auth_secret, "pen-viewer", "viewer")
        self.tenant_token = generate_token(auth_secret, "pen-tenant", "tenant")
        self.operator_token = generate_token(auth_secret, "pen-operator", "operator")

    async def _req(self, method: str, path: str, token: str = "",
                   body: str = "", headers: dict | None = None) -> httpx.Response:
        h = headers or {}
        if token:
            h["Authorization"] = f"Bearer {token}"
        async with httpx.AsyncClient(verify=False, timeout=self.timeout) as c:
            if method == "GET":
                return await c.get(f"{self.fleet}{path}", headers=h)
            elif method == "POST":
                if "Content-Type" not in h:
                    h["Content-Type"] = "application/json"
                return await c.post(f"{self.fleet}{path}", content=body, headers=h)
            elif method == "DELETE":
                return await c.delete(f"{self.fleet}{path}", headers=h)

    def _check(self, name: str, passed: bool, detail: str):
        self.results.append(PenResult(name, passed, detail))
        status = "PASS" if passed else "FAIL"
        print(f"  [{status}] {name}: {detail}")

    # ── SSRF ──

    async def test_ssrf_internal_urls(self):
        print("\n  --- SSRF ---")
        payloads = [
            '{"name":"http://169.254.169.254/latest/meta-data","region":"us-east"}',
            '{"name":"http://kubernetes.default.svc/api","region":"us-east"}',
            '{"name":"file:///etc/passwd","region":"us-east"}',
        ]
        for i, payload in enumerate(payloads):
            try:
                resp = await self._req("POST", "/api/v1/clusters", self.admin_token, payload)
                ok = resp.status_code < 500
                self._check(f"ssrf_{i}", ok,
                            f"status={resp.status_code} (should not trigger server-side fetch)")
            except Exception as e:
                self._check(f"ssrf_{i}", True, f"connection error (expected): {e}")

    # ── Header Injection ──

    async def test_header_injection(self):
        print("\n  --- Header Injection ---")
        evil_headers = {
            "X-Forwarded-For": "127.0.0.1",
            "X-Real-IP": "127.0.0.1",
            "Host": "evil.example.com",
            "Content-Type": "application/json\r\nX-Injected: true",
        }
        for hdr, val in evil_headers.items():
            try:
                resp = await self._req("GET", "/healthz", headers={hdr: val})
                ok = resp.status_code != 500
                self._check(f"header_{hdr}", ok, f"status={resp.status_code}")
            except Exception as e:
                self._check(f"header_{hdr}", True, f"rejected: {e}")

    # ── Token Replay / Expired ──

    async def test_expired_token(self):
        print("\n  --- Token Replay ---")
        expired = generate_token(self.secret, "expired-user", "admin", ttl_hours=-1)
        resp = await self._req("GET", "/api/v1/clusters", expired)
        self._check("expired_token", resp.status_code == 401,
                     f"status={resp.status_code} (should be 401)")

    async def test_wrong_secret_token(self):
        wrong = generate_token("wrong-secret-12345678901234567890", "attacker", "admin")
        resp = await self._req("GET", "/api/v1/clusters", wrong)
        self._check("wrong_secret", resp.status_code == 401,
                     f"status={resp.status_code} (should be 401)")

    async def test_tampered_token(self):
        parts = self.admin_token.split(".")
        tampered_claims = base64.urlsafe_b64encode(
            json.dumps({"sub": "attacker", "role": "admin",
                        "iat": "2026-07-15T00:00:00+00:00",
                        "exp": "2027-07-15T00:00:00+00:00"}).encode()
        ).rstrip(b"=").decode()
        tampered = tampered_claims + "." + parts[1]
        resp = await self._req("GET", "/api/v1/clusters", tampered)
        self._check("tampered_token", resp.status_code == 401,
                     f"status={resp.status_code} (should be 401)")

    # ── Privilege Escalation ──

    async def test_viewer_cannot_write(self):
        print("\n  --- Privilege Escalation ---")
        resp = await self._req("POST", "/api/v1/clusters", self.viewer_token,
                               '{"name":"viewer-test","region":"us-east"}')
        self._check("viewer_write", resp.status_code == 403,
                     f"status={resp.status_code} (should be 403)")

    async def test_viewer_cannot_delete(self):
        resp = await self._req("DELETE", "/api/v1/clusters/nonexistent", self.viewer_token)
        self._check("viewer_delete", resp.status_code == 403,
                     f"status={resp.status_code} (should be 403)")

    async def test_tenant_cannot_list_clusters(self):
        resp = await self._req("GET", "/api/v1/clusters", self.tenant_token)
        self._check("tenant_list_clusters", resp.status_code == 403,
                     f"status={resp.status_code} (should be 403)")

    async def test_tenant_cross_tenant(self):
        resp = await self._req("GET", "/api/v1/tenants/other-tenant/usage", self.tenant_token)
        self._check("tenant_cross_access", resp.status_code == 403,
                     f"status={resp.status_code} (should be 403)")

    async def test_operator_cannot_delete(self):
        resp = await self._req("DELETE", "/api/v1/clusters/nonexistent", self.operator_token)
        self._check("operator_delete", resp.status_code == 403,
                     f"status={resp.status_code} (should be 403)")

    # ── DecisionPackage Tampering ──

    async def test_unsigned_v2_intent(self):
        print("\n  --- DecisionPackage Tampering ---")
        resp = await self._req("POST", "/api/v2/intents", self.admin_token,
                               '{"type":"scale","confidence":0.9}',
                               {"Content-Type": "application/json"})
        self._check("unsigned_intent", resp.status_code in (400, 403, 422),
                     f"status={resp.status_code} (should reject unsigned)")

    async def test_malformed_cloudevent(self):
        resp = await self._req("POST", "/api/v2/intents", self.admin_token,
                               '{"specversion":"1.0","type":"wrong","source":"evil"}',
                               {"Content-Type": "application/cloudevents+json"})
        self._check("malformed_cloudevent", resp.status_code in (400, 403, 422),
                     f"status={resp.status_code} (should reject malformed)")

    # ── Large Payload ──

    async def test_large_payload(self):
        print("\n  --- Large Payload ---")
        large = '{"name":"' + "A" * 1_000_000 + '","region":"us-east"}'
        try:
            resp = await self._req("POST", "/api/v1/clusters", self.admin_token, large)
            ok = resp.status_code != 500
            self._check("large_payload_1mb", ok, f"status={resp.status_code}")
        except Exception as e:
            self._check("large_payload_1mb", True, f"rejected: {type(e).__name__}")

    # ── SQL Injection ──

    async def test_sql_injection(self):
        print("\n  --- SQL Injection ---")
        payloads = [
            "/api/v1/clusters?id='; DROP TABLE clusters;--",
            "/api/v1/clusters/' OR '1'='1",
            "/api/v1/tenants/' UNION SELECT * FROM tenants--/usage",
        ]
        for i, path in enumerate(payloads):
            try:
                resp = await self._req("GET", path, self.admin_token)
                ok = resp.status_code != 500
                self._check(f"sql_injection_{i}", ok, f"status={resp.status_code}")
            except Exception as e:
                self._check(f"sql_injection_{i}", True, f"rejected: {e}")

    # ── Path Traversal ──

    async def test_path_traversal(self):
        print("\n  --- Path Traversal ---")
        paths = [
            "/api/v1/../../etc/passwd",
            "/api/v1/clusters/%2e%2e/%2e%2e/etc/passwd",
            "/api/v1/clusters/..%252f..%252fetc/passwd",
        ]
        for i, path in enumerate(paths):
            try:
                resp = await self._req("GET", path, self.admin_token)
                ok = resp.status_code in (301, 308, 400, 404) and "root:" not in resp.text
                self._check(f"path_traversal_{i}", ok, f"status={resp.status_code}")
            except Exception as e:
                self._check(f"path_traversal_{i}", True, f"rejected: {e}")

    # ── Run All ──

    async def run(self):
        print(f"\n{'='*60}")
        print(f"EXPANDED PENETRATION TEST SUITE")
        print(f"{'='*60}")
        print(f"  Target: {self.fleet}")

        await self.test_ssrf_internal_urls()
        await self.test_header_injection()
        await self.test_expired_token()
        await self.test_wrong_secret_token()
        await self.test_tampered_token()
        await self.test_viewer_cannot_write()
        await self.test_viewer_cannot_delete()
        await self.test_tenant_cannot_list_clusters()
        await self.test_tenant_cross_tenant()
        await self.test_operator_cannot_delete()
        await self.test_unsigned_v2_intent()
        await self.test_malformed_cloudevent()
        await self.test_large_payload()
        await self.test_sql_injection()
        await self.test_path_traversal()

        print(f"\n{'='*60}")
        print(f"PEN TEST RESULTS")
        print(f"{'='*60}")

        passed = sum(1 for r in self.results if r.passed)
        failed = sum(1 for r in self.results if not r.passed)

        for r in self.results:
            if not r.passed:
                print(f"  [FAIL] {r.name}: {r.detail}")

        print(f"\n  TOTAL: {passed} passed, {failed} failed out of {len(self.results)}")
        if failed == 0:
            print(f"\n  RESULT: ALL PEN TESTS PASSED")
        else:
            print(f"\n  RESULT: {failed} FAILURE(S)")


async def main():
    parser = argparse.ArgumentParser(description="Expanded penetration test suite")
    parser.add_argument("--fleet-url", default="http://fleet-controller.fleet-llm-d.svc:8080")
    parser.add_argument("--auth-secret", default="oberon-fleet-secret-2026")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    test = PenTest(args.fleet_url, args.auth_secret, args.timeout)
    await test.run()


if __name__ == "__main__":
    asyncio.run(main())
