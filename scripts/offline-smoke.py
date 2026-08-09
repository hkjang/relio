#!/usr/bin/env python3
import http.cookiejar
import json
import re
import sys
import urllib.error
import urllib.request


if len(sys.argv) != 4:
    raise SystemExit("usage: offline-smoke.py <base-url> <admin> <password>")

base_url, admin, initial_password = sys.argv[1].rstrip("/"), sys.argv[2], sys.argv[3]
cookie_jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookie_jar))


def request(path, method="GET", body=None, headers=None, expect_json=True):
    data = json.dumps(body).encode() if body is not None else None
    request_headers = dict(headers or {})
    if body is not None:
        request_headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base_url + path, data=data, headers=request_headers, method=method)
    try:
        with opener.open(req, timeout=15) as response:
            content = response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
    return json.loads(content) if expect_json else content.decode()


def expect_http_error(path, method="GET", body=None, headers=None, contains=None):
    try:
        request(path, method, body, headers)
    except RuntimeError as exc:
        if contains is not None:
            assert contains in str(exc), str(exc)
        return
    raise AssertionError(f"{method} {path} unexpectedly succeeded")


ready = request("/health/ready")
assert ready["status"] == "ready"
login = request("/api/v1/auth/login", "POST", {"username": admin, "password": initial_password})
csrf = login["user"]["csrfToken"]
csrf_header = {"X-CSRF-Token": csrf}
request(
    "/api/v1/me/password",
    "POST",
    {"currentPassword": initial_password, "newPassword": "Relio-Smoke-Password-2026"},
    csrf_header,
)
sessions = request("/api/v1/me/sessions")
assert len(sessions["items"]) == 1 and sessions["items"][0]["current"] is True
customer = request(
    "/api/v1/customers",
    "POST",
    {"name": "Offline Verification Customer", "customerType": "PROSPECT", "industry": "Verification", "customFields": {}},
    {**csrf_header, "Idempotency-Key": "offline-customer-0001"},
)
same_customer = request(
    "/api/v1/customers",
    "POST",
    {"name": "Offline Verification Customer", "customerType": "PROSPECT", "industry": "Verification", "customFields": {}},
    {**csrf_header, "Idempotency-Key": "offline-customer-0001"},
)
assert same_customer["id"] == customer["id"]
duplicate_customer = request(
    "/api/v1/customers",
    "POST",
    {"name": "Offline Verification Customer", "customerType": "PROSPECT", "industry": "Duplicate", "customFields": {}},
    csrf_header,
)
duplicates = request(f"/api/v1/customers/{customer['id']}/duplicates")
assert duplicate_customer["id"] in {item["id"] for item in duplicates["items"]}
request(
    f"/api/v1/customers/{customer['id']}/merge",
    "POST",
    {"sourceIds": [duplicate_customer["id"]]},
    csrf_header,
)
pipeline = request("/api/v1/pipeline")
stages = pipeline["items"][0]["stages"]
assert len(stages) >= 2
stage_id = stages[0]["id"]
next_stage_id = stages[1]["id"]
opportunity = request(
    "/api/v1/opportunities",
    "POST",
    {"name": "Offline Verification Deal", "customerId": customer["id"], "stageId": stage_id, "expectedAmount": 100000000, "customFields": {}},
    csrf_header,
)
health = request(f"/api/v1/opportunities/{opportunity['id']}/health")
assert health["riskScore"] >= 40
assert {factor["code"] for factor in health["factors"]}.issuperset({"NO_NEXT_ACTION", "NO_DECISION_MAKER", "NO_CHAMPION"})
health_rules = request("/api/v1/admin/deal-health-rules")["items"]
next_action_rule = next(rule for rule in health_rules if rule["code"] == "NO_NEXT_ACTION")
updated_rule = request(
    f"/api/v1/admin/deal-health-rules/{next_action_rule['id']}",
    "PUT",
    {
        "name": next_action_rule["name"],
        "description": next_action_rule["description"],
        "threshold": next_action_rule["threshold"],
        "riskScore": next_action_rule["riskScore"] + 1,
        "recommendedAction": next_action_rule["recommendedAction"],
        "active": True,
        "priority": next_action_rule["priority"],
        "version": next_action_rule["version"],
    },
    csrf_header,
)
assert updated_rule["version"] == next_action_rule["version"] + 1
expect_http_error(
    f"/api/v1/admin/deal-health-rules/{next_action_rule['id']}",
    "PUT",
    {
        "name": next_action_rule["name"],
        "description": next_action_rule["description"],
        "threshold": next_action_rule["threshold"],
        "riskScore": next_action_rule["riskScore"],
        "recommendedAction": next_action_rule["recommendedAction"],
        "active": True,
        "priority": next_action_rule["priority"],
        "version": next_action_rule["version"],
    },
    csrf_header,
    "changed by another administrator",
)
inspection = request(f"/api/v1/opportunities/{opportunity['id']}/inspection?days=7")
assert inspection["health"]["opportunityId"] == opportunity["id"]
execution = request(
    f"/api/v1/admin/stages/{stage_id}/sales-execution",
    "PUT",
    {
        "playbookName": "Offline Qualification Playbook",
        "guidance": "Advance only after the required customer check is complete.",
        "active": True,
        "items": [
            {
                "title": "Confirm decision process",
                "description": "Record the customer's decision process.",
                "itemType": "CHECKLIST",
                "required": True,
                "displayOrder": 10,
            }
        ],
        "criteria": [
            {
                "name": "Required playbook complete",
                "criterionType": "PLAYBOOK_COMPLETE",
                "operator": "PRESENT",
                "expectedValue": {},
                "enforcement": "BLOCK",
                "message": "Complete the required sales playbook before advancing.",
                "active": True,
                "displayOrder": 10,
            }
        ],
    },
    csrf_header,
)
assert execution["playbook"]["name"] == "Offline Qualification Playbook"
playbook = request(f"/api/v1/opportunities/{opportunity['id']}/playbook")
assert playbook["requiredTotal"] == 1 and playbook["requiredDone"] == 0
readiness = request(f"/api/v1/opportunities/{opportunity['id']}/stage-readiness?stageId={next_stage_id}")
assert readiness["allowed"] is False and len(readiness["blocked"]) == 1
expect_http_error(
    f"/api/v1/opportunities/{opportunity['id']}/stage",
    "POST",
    {"stageId": next_stage_id, "version": opportunity["version"]},
    csrf_header,
    "stage exit criteria blocked",
)
playbook = request(
    f"/api/v1/opportunities/{opportunity['id']}/playbook/{playbook['items'][0]['id']}",
    "PUT",
    {"completed": True, "notes": "verified in offline smoke"},
    csrf_header,
)
assert playbook["requiredDone"] == 1
# Re-saving an unchanged administrator policy must preserve user completion history.
execution = request(
    f"/api/v1/admin/stages/{stage_id}/sales-execution",
    "PUT",
    {
        "playbookName": execution["playbook"]["name"],
        "guidance": execution["playbook"]["guidance"],
        "active": execution["playbook"]["active"],
        "items": [
            {
                "id": item["id"],
                "title": item["title"],
                "description": item.get("description", ""),
                "itemType": item["itemType"],
                "fieldKey": item.get("fieldKey", ""),
                "required": item["required"],
                "displayOrder": item["displayOrder"],
            }
            for item in execution["playbook"]["items"]
        ],
        "criteria": execution["criteria"],
    },
    csrf_header,
)
playbook = request(f"/api/v1/opportunities/{opportunity['id']}/playbook")
assert playbook["requiredDone"] == 1
readiness = request(f"/api/v1/opportunities/{opportunity['id']}/stage-readiness?stageId={next_stage_id}")
assert readiness["allowed"] is True
opportunity = request(
    f"/api/v1/opportunities/{opportunity['id']}/stage",
    "POST",
    {"stageId": next_stage_id, "version": opportunity["version"]},
    csrf_header,
)
assert opportunity["stageId"] == next_stage_id
override = request(
    f"/api/v1/forecasts/overrides/{opportunity['id']}",
    "PUT",
    {"forecastCategory": "COMMIT", "probability": 85, "amount": 90000000, "reason": "offline manager verification", "version": 1},
    csrf_header,
)
assert override["forecastCategory"] == "COMMIT"
expect_http_error(
    f"/api/v1/forecasts/overrides/{opportunity['id']}",
    "PUT",
    {"forecastCategory": "BEST_CASE", "reason": "stale offline manager verification", "version": 0},
    csrf_header,
    "changed by another manager",
)
forecast_intelligence = request("/api/v1/forecasts/intelligence?days=7")
assert forecast_intelligence["currentAmount"] >= 100000000
assert forecast_intelligence["managerCommit"] >= 90000000
coaching = request("/api/v1/deal-intelligence/coaching")
assert coaching["owners"] and coaching["owners"][0]["openDeals"] >= 1
approval_status = request("/api/v1/approvals/status")
assert approval_status["enabled"] is False
key = request(
    "/api/v1/me/keys",
    "POST",
    {
        "name": "Offline MCP Verification",
        "scopes": ["mcp:use", "customer:read", "opportunity:read", "forecast:read", "approval:request", "approval:approve"],
        "channels": ["REST", "MCP"],
    },
    csrf_header,
)
inventory = request("/api/v1/admin/personal-keys")
assert inventory["items"][0]["keyId"] == key["key"]["keyId"]
assert "secret" not in inventory["items"][0]
bearer = {"Authorization": "Bearer " + key["secret"]}
customers = request("/api/v1/customers?limit=1", headers=bearer)
assert isinstance(customers["items"], list)
projected = request("/api/v1/customers?sort=name&fields=id,name&limit=1", headers=bearer)
assert set(projected["items"][0]) == {"id", "name"}
mcp_headers = {
    **bearer,
    "Accept": "application/json, text/event-stream",
    "Content-Type": "application/json",
}
initialized = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "offline-smoke", "version": "1"}}},
    mcp_headers,
)
assert initialized["result"]["serverInfo"]["name"] == "Relio"
tool_list = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
tool_names = {tool["name"] for tool in tool_list["result"]["tools"]}
assert "submit_approval" not in tool_names
assert {"find_deals_at_risk", "explain_deal_risk", "explain_forecast_change", "get_sales_coaching_insights"}.issubset(tool_names)
tool_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "search_customers", "arguments": {"query": "Offline Verification"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert tool_result["result"]["isError"] is False
deal_risk_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 31, "method": "tools/call", "params": {"name": "explain_deal_risk", "arguments": {"id": opportunity["id"]}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert deal_risk_result["result"]["isError"] is False
request(
    "/api/v1/admin/approval-policies",
    "POST",
    {
        "name": "Offline high-value deal review",
        "entityType": "OPPORTUNITY",
        "conditionField": "expected_amount",
        "conditionOperator": "GTE",
        "conditionValue": 50000000,
        "approverMethod": "MANAGER",
        "approvalSteps": 1,
        "allowReject": True,
        "allowResubmit": True,
        "active": True,
        "priority": 100,
    },
    csrf_header,
)
assert request("/api/v1/approvals/status")["enabled"] is True
enabled_tools = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 4, "method": "tools/list", "params": {}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert "submit_approval" in {tool["name"] for tool in enabled_tools["result"]["tools"]}
approval = request(
    "/api/v1/approvals",
    "POST",
    {"entityType": "OPPORTUNITY", "entityId": opportunity["id"], "reason": "offline approval verification"},
    csrf_header,
)
approved = request(
    f"/api/v1/approvals/{approval['id']}/approve",
    "POST",
    {"version": approval["version"], "comment": "verified"},
    csrf_header,
)
assert approved["status"] == "APPROVED"
request(
    "/api/v1/admin/settings/mcp/tool_allowlist",
    "PUT",
    {"value": ["get_customer"], "valueType": "json", "version": 1},
    csrf_header,
)
restricted_tools = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 5, "method": "tools/list", "params": {}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert {tool["name"] for tool in restricted_tools["result"]["tools"]} == {"get_customer"}
blocked_tool = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": {"name": "search_customers", "arguments": {}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert "error" in blocked_tool
rotated = request(
    f"/api/v1/me/keys/{key['key']['id']}/rotate",
    "POST",
    {},
    csrf_header,
)
# The old key remains valid during the configured grace period, and the new key is active immediately.
assert request("/api/v1/customers?limit=1", headers=bearer)["items"]
assert request("/api/v1/customers?limit=1", headers={"Authorization": "Bearer " + rotated["secret"]})["items"]
revoked = request(f"/api/v1/admin/users/{key['key'].get('userId', login['user']['id'])}/keys/revoke-all", "POST", {}, csrf_header)
assert revoked["revoked"] == 2
html = request("/app/dashboard", expect_json=False)
assert not re.search(r"<(script|link|img)[^>]+(src|href)=['\"]https?://", html, re.IGNORECASE)
build = request("/api/v1/system/version")
assert build["name"] == "Relio"
# Disabling ordinary local login must never lock out the Bootstrap break-glass administrator.
request(
    "/api/v1/admin/settings/auth/local_login_enabled",
    "PUT",
    {"value": False, "valueType": "boolean", "version": 1},
    csrf_header,
)
request("/api/v1/auth/logout", "POST", {}, csrf_header, expect_json=False)
break_glass = request("/api/v1/auth/login", "POST", {"username": admin, "password": "Relio-Smoke-Password-2026"})
assert break_glass["user"]["isBootstrap"] is True
print("Relio offline smoke test passed")
