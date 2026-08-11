#!/usr/bin/env python3
import http.cookiejar
import json
import os
import sys
import urllib.error
import urllib.request


if len(sys.argv) not in (6, 7) or sys.argv[1] not in ("setup", "verify"):
    raise SystemExit(
        "usage: restart-persistence.py <setup|verify> <base-url> <admin> <password> <state-file>"
        " [new-password|portable]"
    )

mode = sys.argv[1]
base_url = sys.argv[2].rstrip("/")
admin = sys.argv[3]
password = sys.argv[4]
state_path = sys.argv[5]
extra = sys.argv[6] if len(sys.argv) == 7 else ""
# In setup mode the extra argument is the first-login password; in verify mode it
# asserts that the data key is wrapped by ENCRYPTION_KEY rather than the volume.
new_password = extra if mode == "setup" else ""
expect_portable = mode == "verify" and extra == "portable"
cookie_jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookie_jar))


def request(path, method="GET", body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    request_headers = dict(headers or {})
    if body is not None:
        request_headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base_url + path, data=data, headers=request_headers, method=method)
    try:
        with opener.open(req, timeout=20) as response:
            content = response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
    return json.loads(content) if content else None


login = request("/api/v1/auth/login", "POST", {"username": admin, "password": password})
csrf_header = {"X-CSRF-Token": login["user"]["csrfToken"]}
if login["user"].get("mustChangePassword"):
    if not new_password:
        raise AssertionError("new-password is required for a first-login persistence setup")
    request(
        "/api/v1/me/password",
        "POST",
        {"currentPassword": password, "newPassword": new_password},
        csrf_header,
    )


if mode == "setup":
    config = request("/api/v1/admin/oidc")
    body = {
        "enabled": False,
        "issuerUrl": "https://keycloak.persistence.invalid/realms/relio",
        "clientId": "relio-persistence-test",
        "clientSecret": "Relio-Persistent-OIDC-Secret-2026",
        "scopes": ["openid", "profile", "email"],
        "usernameClaim": "preferred_username",
        "emailClaim": "email",
        "nameClaim": "name",
        "groupClaim": "groups",
        "roleClaim": "realm_access.roles",
        "autoProvision": False,
        "defaultRoleId": "",
        "rootCaPem": "",
    }
    if "version" in config:
        body["version"] = config["version"]
    saved = request("/api/v1/admin/oidc", "PUT", body, csrf_header)
    assert saved["clientSecretConfigured"] is True
    created = request(
        "/api/v1/me/keys",
        "POST",
        {
            "name": "Restart Continuity Verification",
            "scopes": ["customer:read"],
            "channels": ["REST"],
        },
        csrf_header,
    )
    operations = request("/api/v1/admin/operations")
    state = {
        "personalKey": created["secret"],
        "keyId": created["key"]["keyId"],
        "masterKeyId": operations.get("secrets", {}).get("keyId", ""),
        "oidcVersion": saved.get("version", 0),
    }
    with open(state_path, "w", encoding="utf-8") as handle:
        json.dump(state, handle)
    os.chmod(state_path, 0o600)
    print("Protected credential persistence setup passed")
else:
    with open(state_path, encoding="utf-8") as handle:
        state = json.load(handle)
    config = request("/api/v1/admin/oidc")
    assert config["clientSecretConfigured"] is True
    assert config["clientId"] == "relio-persistence-test"
    if state["oidcVersion"]:
        assert config["version"] == state["oidcVersion"]
    # Saving a masked form with a blank Client Secret must preserve the
    # encrypted value, increment the optimistic version and remain decryptable.
    preserved = dict(config)
    preserved["clientSecret"] = ""
    for response_only in (
        "id",
        "clientSecretConfigured",
        "callbackUrl",
        "discovery",
        "lastTestedAt",
        "lastTestResult",
    ):
        preserved.pop(response_only, None)
    saved = request("/api/v1/admin/oidc", "PUT", preserved, csrf_header)
    assert saved["clientSecretConfigured"] is True
    assert saved["version"] == config["version"] + 1
    test_result = request("/api/v1/admin/oidc/test", "POST", {}, csrf_header)
    assert "issuer" in test_result["checks"]
    bearer = {"Authorization": "Bearer " + state["personalKey"]}
    assert isinstance(request("/api/v1/customers?limit=1", headers=bearer)["items"], list)
    operations = request("/api/v1/admin/operations")
    assert operations["secrets"]["matches"] is True
    assert operations["secrets"]["registered"] is True
    assert operations["secrets"]["protectedCredentials"] >= 2
    if state["masterKeyId"]:
        # The data key must be the same one that encrypted the credentials in
        # setup mode. A changed id means something silently re-keyed the
        # instance and every Personal Key would have to be re-issued.
        assert operations["secrets"]["keyId"] == state["masterKeyId"], (
            f"data key changed: {state['masterKeyId']} -> {operations['secrets']['keyId']}"
        )
    if expect_portable:
        assert operations["secrets"]["portable"] is True, "data key is still tied to the data volume"
        assert operations["secrets"]["wrapOrigin"] == "ENCRYPTION_KEY"
        assert operations["secrets"]["envConfigured"] is True
    print("Protected credentials survived container replacement")
