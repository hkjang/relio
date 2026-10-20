#!/usr/bin/env python3
import http.cookiejar
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import date


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


def authenticated_client(username, password, next_password):
    """Create an isolated browser session and clear the first-login password gate."""
    session_jar = http.cookiejar.CookieJar()
    session_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(session_jar))

    def call(path, method="GET", body=None, headers=None):
        data = json.dumps(body).encode() if body is not None else None
        request_headers = dict(headers or {})
        if body is not None:
            request_headers["Content-Type"] = "application/json"
        req = urllib.request.Request(base_url + path, data=data, headers=request_headers, method=method)
        try:
            with session_opener.open(req, timeout=15) as response:
                content = response.read()
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode(errors="replace")
            raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
        return json.loads(content)

    signed_in = call("/api/v1/auth/login", "POST", {"username": username, "password": password})
    session_headers = {"X-CSRF-Token": signed_in["user"]["csrfToken"]}
    if signed_in["user"]["mustChangePassword"]:
        call(
            "/api/v1/me/password",
            "POST",
            {"currentPassword": password, "newPassword": next_password},
            session_headers,
        )
    return call, session_headers


def expect_client_error(call, path, method="GET", body=None, headers=None, contains=None):
    try:
        call(path, method, body, headers)
    except RuntimeError as exc:
        if contains is not None:
            assert contains in str(exc), str(exc)
        return
    raise AssertionError(f"{method} {path} unexpectedly succeeded")


def assert_mcp_schema(schema, value, path="arguments"):
    """Check the emitted schema subset before HTTP, like a strict gateway.

    The prior suite sent tools/call directly, bypassing nested JSON Schema
    validation entirely; a server-only success did not prove client usability.
    This dependency-free check keeps the offline test image self-contained.
    """
    if "anyOf" in schema:
        for choice in schema["anyOf"]:
            try:
                assert_mcp_schema(choice, value, path)
                break
            except AssertionError:
                pass
        else:
            raise AssertionError(f"{path}: no anyOf variant accepted {value!r}")
    kind = schema.get("type")
    checks = {
        "object": lambda v: isinstance(v, dict),
        "array": lambda v: isinstance(v, list),
        "string": lambda v: isinstance(v, str),
        "number": lambda v: type(v) in (int, float),
        "integer": lambda v: type(v) is int,
        "boolean": lambda v: type(v) is bool,
    }
    if kind:
        assert kind in checks and checks[kind](value), f"{path}: expected {kind}"
    if "enum" in schema:
        assert value in schema["enum"], f"{path}: invalid enum value {value!r}"
    if kind == "object":
        props = schema.get("properties", {})
        for key in schema.get("required", []):
            assert key in value, f"{path}.{key}: required"
        for key, child in value.items():
            if key in props:
                assert_mcp_schema(props[key], child, f"{path}.{key}")
            else:
                assert schema.get("additionalProperties", True) is not False, f"{path}.{key}: not allowed"
    if kind == "array":
        for index, child in enumerate(value):
            assert_mcp_schema(schema.get("items", {}), child, f"{path}[{index}]")


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
# This suite issues several hundred requests in well under a minute, which is a
# property of the test rather than of real usage, so it would trip the default
# 120/min API throttle partway through. Lift it for the run. The limiter itself is
# covered by TestRequestLimiterCountsPerIdentityAndWindow.
request(
    "/api/v1/admin/settings/api/rate_limit_per_minute",
    "PUT",
    {"value": 0, "valueType": "number"},
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
decision_maker = request(
    "/api/v1/contacts",
    "POST",
    {"customerId": customer["id"], "name": "Park Executive", "title": "CIO", "decisionMaker": True, "relationshipRole": "DECISION_MAKER", "influence": "HIGH", "sentiment": "NEUTRAL", "relationshipStrength": 55, "decisionPower": 95},
    csrf_header,
)
champion = request(
    "/api/v1/contacts",
    "POST",
    {"customerId": customer["id"], "name": "Kim Champion", "title": "Director", "primaryContact": True, "relationshipRole": "CHAMPION", "influence": "HIGH", "sentiment": "SUPPORT", "relationshipStrength": 85, "decisionPower": 70},
    csrf_header,
)
influencer = request(
    "/api/v1/contacts",
    "POST",
    {"customerId": customer["id"], "name": "Lee Architect", "title": "Team Lead", "relationshipRole": "INFLUENCER", "influence": "MEDIUM", "sentiment": "SUPPORT", "relationshipStrength": 70, "decisionPower": 55},
    csrf_header,
)
relationship = request(
    f"/api/v1/customers/{customer['id']}/relationships",
    "POST",
    {"sourceContactId": champion["id"], "targetContactId": decision_maker["id"], "relationshipType": "INFLUENCES", "strength": 90, "description": "Internal champion influences the economic buyer", "active": True, "version": 0},
    csrf_header,
)
assert relationship["version"] == 1 and relationship["sourceName"] == champion["name"]
relationship = request(
    f"/api/v1/customers/{customer['id']}/relationships/{relationship['id']}",
    "PUT",
    {"sourceContactId": champion["id"], "targetContactId": decision_maker["id"], "relationshipType": "INFLUENCES", "strength": 92, "description": "Verified influence path", "active": True, "version": relationship["version"]},
    csrf_header,
)
assert relationship["version"] == 2 and relationship["strength"] == 92
expect_http_error(
    f"/api/v1/customers/{customer['id']}/relationships/{relationship['id']}",
    "PUT",
    {"sourceContactId": champion["id"], "targetContactId": decision_maker["id"], "relationshipType": "INFLUENCES", "strength": 40, "description": "stale", "active": True, "version": 1},
    csrf_header,
    "changed by another user",
)
request(
    f"/api/v1/customers/{customer['id']}/relationships",
    "POST",
    {"sourceContactId": influencer["id"], "targetContactId": champion["id"], "relationshipType": "TRUSTS", "strength": 75, "description": "Technical trust", "active": True, "version": 0},
    csrf_header,
)
graph = request(f"/api/v1/customers/{customer['id']}/relationships")
assert graph["metrics"]["decisionMakers"] == 1 and graph["metrics"]["champions"] == 1
assert graph["metrics"]["relationshipScore"] >= 80 and len(graph["edges"]) == 2
plan_year = date.today().year
account_plan = request(
    f"/api/v1/customers/{customer['id']}/account-plan",
    "PUT",
    {"planYear": plan_year, "status": "ACTIVE", "strategy": "Expand from CRM into revenue intelligence", "customerGoals": ["Improve forecast accuracy"], "strategicInitiatives": ["Enterprise CRM modernization"], "ourObjectives": ["Establish Relio as system of action"], "whiteSpaces": [{"productName": "Revenue Intelligence", "status": "NOT_OFFERED", "potentialAmount": 250000000, "notes": "Validate in Q4"}, {"productName": "CRM Core", "status": "CUSTOMER", "potentialAmount": 0}], "competitors": ["Legacy CRM"], "risks": ["Budget timing"], "targetRevenue": 500000000, "potentialRevenue": 750000000, "version": 0},
    csrf_header,
)
assert account_plan["version"] == 1 and account_plan["status"] == "ACTIVE"
cross_sell = request(f"/api/v1/customers/{customer['id']}/cross-sell?year={plan_year}")
assert cross_sell["count"] == 1 and cross_sell["items"][0]["productName"] == "Revenue Intelligence"
relationship_settings = request("/api/v1/admin/settings?namespace=relationship_intelligence")["items"]
assert {item["key"] for item in relationship_settings} == {"graph_max_nodes", "default_plan_year", "allowed_opportunity_roles"}
pipeline = request("/api/v1/pipeline")
stages = pipeline["items"][0]["stages"]
assert len(stages) >= 2
stage_id = stages[0]["id"]
next_stage_id = stages[1]["id"]
opportunity = request(
    "/api/v1/opportunities",
    "POST",
    {"name": "Offline Verification Deal", "customerId": customer["id"], "stageId": stage_id, "expectedAmount": 100000, "currencyCode": "USD", "exchangeRate": 1000, "customFields": {}},
    csrf_header,
)
assert opportunity["currencyCode"] == "USD" and opportunity["baseExpectedAmount"] == 100000000
forecast = request("/api/v1/forecasts")
usd = next(item for item in forecast["currencies"] if item["currencyCode"] == "USD")
assert usd["nativeAmount"] >= 100000 and usd["baseAmount"] >= 100000000
contract = request(
    "/api/v1/contracts",
    "POST",
    {"customerId": customer["id"], "opportunityId": opportunity["id"], "title": "Offline Multi-Currency Subscription", "amount": 1200, "currencyCode": "USD", "exchangeRate": 1000, "startDate": f"{plan_year}-01-31", "endDate": f"{plan_year}-04-30", "status": "DRAFT", "autoRenew": True, "revenueScheduleType": "MONTHLY", "renewalNoticeDays": 90, "renewalAction": "Run renewal QBR", "customFields": {}},
    csrf_header,
)
assert contract["status"] == "DRAFT" and contract["baseAmount"] == 1200000
contract = request(
    f"/api/v1/contracts/{contract['id']}/activate",
    "POST",
    {"version": contract["version"]},
    csrf_header,
)
assert contract["status"] == "ACTIVE" and contract["activatedAt"]
revenue_schedule = request(f"/api/v1/contracts/{contract['id']}/revenue-schedule")["items"]
assert len(revenue_schedule) == 4
assert round(sum(item["amount"] for item in revenue_schedule), 2) == 1200
recognized = request(
    f"/api/v1/revenue-schedules/{revenue_schedule[0]['id']}/recognize",
    "POST",
    {"recognizedDate": f"{plan_year}-01-31"},
    csrf_header,
)
assert recognized["status"] == "RECOGNIZED" and recognized["recognizedSaleId"]
contracts = request("/api/v1/contracts?limit=100")["items"]
created_contract = next(item for item in contracts if item["id"] == contract["id"])
assert created_contract["scheduleCount"] == 4 and created_contract["recognizedCount"] == 1
collaborator = request(
    "/api/v1/admin/users",
    "POST",
    {"username": "offline-presales", "displayName": "Offline Presales", "email": "presales@offline.example", "password": "Offline-Presales-2026", "title": "Presales Architect", "roleIds": []},
    csrf_header,
)
collaborators = request("/api/v1/collaborators?limit=200")["items"]
assert collaborator["id"] in {item["id"] for item in collaborators}
team_member = request(
    f"/api/v1/opportunities/{opportunity['id']}/team/{collaborator['id']}",
    "PUT",
    {"role": "PRESALES", "responsibility": "Own technical validation", "version": 0},
    csrf_header,
)
assert team_member["version"] == 1 and team_member["role"] == "PRESALES"
team_member = request(
    f"/api/v1/opportunities/{opportunity['id']}/team/{collaborator['id']}",
    "PUT",
    {"role": "CONSULTANT", "responsibility": "Lead discovery workshop", "version": team_member["version"]},
    csrf_header,
)
assert team_member["version"] == 2 and team_member["role"] == "CONSULTANT"
expect_http_error(
    f"/api/v1/opportunities/{opportunity['id']}/team/{collaborator['id']}",
    "PUT",
    {"role": "LEGAL", "responsibility": "stale", "version": 1},
    csrf_header,
    "changed by another user",
)
team = request(f"/api/v1/opportunities/{opportunity['id']}/team")
assert len(team["items"]) == 1 and team["items"][0]["userId"] == collaborator["id"]
health = request(f"/api/v1/opportunities/{opportunity['id']}/health")
assert health["riskScore"] >= 40
health_codes = {factor["code"] for factor in health["factors"]}
assert "NO_NEXT_ACTION" in health_codes
assert "NO_DECISION_MAKER" not in health_codes and "NO_CHAMPION" not in health_codes
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
key_capabilities = request("/api/v1/me/keys")
catalog = {tool["name"]: tool for tool in key_capabilities["mcpTools"]}
assert catalog["create_customer"]["requiredScopes"] == ["customer:read", "customer:write"]
assert {"create_customer", "update_customer", "delete_customer", "create_contact", "list_leads", "list_products", "list_quotations", "create_contract", "list_sales", "list_targets", "list_notifications"}.issubset(catalog)
assert {"voice:read", "voice:write", "forecast:write"}.issubset(key_capabilities["allowedScopes"])
key = request(
    "/api/v1/me/keys",
    "POST",
    {
        "name": "Offline MCP Verification",
        "scopes": ["mcp:use", "customer:read", "customer:write", "customer:delete", "contact:read", "opportunity:read", "activity:read", "forecast:read", "approval:request", "approval:approve"],
        "channels": ["REST", "MCP"],
    },
    csrf_header,
)
assert key["key"]["version"] == 1
# Access can be narrowed and expanded without rotating the one-time secret.
# The version also prevents a stale browser tab from restoring old privileges.
limited_key = request(
    f"/api/v1/me/keys/{key['key']['id']}",
    "PUT",
    {"version": key["key"]["version"], "scopes": ["mcp:use", "customer:read"], "channels": ["MCP"]},
    csrf_header,
)
assert limited_key["channels"] == ["MCP"] and limited_key["version"] == 2
restored_key = request(
    f"/api/v1/me/keys/{key['key']['id']}",
    "PUT",
    {
        "version": limited_key["version"],
        "scopes": ["mcp:use", "customer:read", "customer:write", "customer:delete", "contact:read", "opportunity:read", "activity:read", "forecast:read", "approval:request", "approval:approve"],
        "channels": ["REST", "MCP"],
    },
    csrf_header,
)
assert restored_key["version"] == 3
expect_http_error(
    f"/api/v1/me/keys/{key['key']['id']}",
    "PUT",
    {"version": limited_key["version"], "scopes": ["customer:read"], "channels": ["REST"]},
    csrf_header,
    contains="another user",
)
key["key"] = restored_key
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
# A client configured with a trailing slash used to fall through to the single
# page shell and receive HTML, which every MCP client reports as
# "failed to parse json". Both spellings must reach the protocol handler.
slash_list = request(
    "/mcp/",
    "POST",
    {"jsonrpc": "2.0", "id": 21, "method": "tools/list", "params": {}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert len(slash_list["result"]["tools"]) == len(tool_list["result"]["tools"])
# The same shell answered OAuth discovery probes with HTML. A protocol client
# has to see a plain 404 so it keeps using the Personal Key it already holds.
# MCP OAuth is off by default: no metadata, and a challenge that names only
# the scheme so clients stay on their Personal Key.
for probe in ("/.well-known/oauth-protected-resource/mcp",):
    expect_http_error(probe, contains="HTTP 404")
for probe in ("/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server"):
    expect_http_error(probe, contains="HTTP 404")
# Neither an unparseable body nor a session teardown may answer 200 with
# something a client will try, and fail, to decode.
expect_http_error("/mcp", "POST", None, {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"}, contains="HTTP 400")
assert request("/mcp", "DELETE", None, mcp_headers, expect_json=False) == ""
prompts = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 22, "method": "prompts/list", "params": {}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert prompts["result"]["prompts"] == []
tool_names = {tool["name"] for tool in tool_list["result"]["tools"]}
assert all(tool["inputSchema"].get("required") is not None for tool in tool_list["result"]["tools"] if "required" in tool["inputSchema"])
assert "submit_approval" not in tool_names
assert {"create_customer", "update_customer", "delete_customer"}.issubset(tool_names)
assert {"find_deals_at_risk", "explain_deal_risk", "explain_forecast_change", "get_sales_coaching_insights"}.issubset(tool_names)
assert {"get_account_brief", "get_account_relationships", "get_account_plan", "find_cross_sell_opportunities", "get_opportunity_team"}.issubset(tool_names)
tool_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "search_customers", "arguments": {"query": "Offline Verification"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert tool_result["result"]["isError"] is False
mcp_customer_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 30, "method": "tools/call", "params": {"name": "create_customer", "arguments": {"name": "Qwen MCP 등록 검증 고객", "industry": "Software", "email": "qwen-mcp@relio.invalid"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert mcp_customer_result["result"]["isError"] is False, mcp_customer_result
mcp_customer = mcp_customer_result["result"]["structuredContent"]
assert mcp_customer["name"] == "Qwen MCP 등록 검증 고객" and mcp_customer["version"] == 1
mcp_customer_updated_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 301, "method": "tools/call", "params": {"name": "update_customer", "arguments": {"id": mcp_customer["id"], "version": mcp_customer["version"], "name": "Qwen MCP 수정 검증 고객"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert mcp_customer_updated_result["result"]["structuredContent"]["name"] == "Qwen MCP 수정 검증 고객"
# Partial MCP updates must preserve omitted CRM fields instead of clearing them.
assert mcp_customer_updated_result["result"]["structuredContent"]["industry"] == "Software"
assert mcp_customer_updated_result["result"]["structuredContent"]["email"] == "qwen-mcp@relio.invalid"
mcp_customer_deleted_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 302, "method": "tools/call", "params": {"name": "delete_customer", "arguments": {"id": mcp_customer["id"]}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert mcp_customer_deleted_result["result"]["structuredContent"]["deleted"] is True
deal_risk_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 31, "method": "tools/call", "params": {"name": "explain_deal_risk", "arguments": {"id": opportunity["id"]}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert deal_risk_result["result"]["isError"] is False
account_brief_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 32, "method": "tools/call", "params": {"name": "get_account_brief", "arguments": {"id": customer["id"], "year": plan_year}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert "result" in account_brief_result, account_brief_result
assert account_brief_result["result"]["structuredContent"]["accountPlan"]["status"] == "ACTIVE"
assert account_brief_result["result"]["structuredContent"]["relationships"]["metrics"]["champions"] == 1
opportunity_team_result = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 33, "method": "tools/call", "params": {"name": "get_opportunity_team", "arguments": {"id": opportunity["id"]}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert "result" in opportunity_team_result, opportunity_team_result
# structuredContent is a JSON object by specification; a list tool wraps its
# rows. A bare array here made every SDK-based client reject the call.
team = opportunity_team_result["result"]["structuredContent"]
assert isinstance(team, dict) and team["count"] == len(team["items"]), team
assert team["items"][0]["role"] == "CONSULTANT"
# The text block keeps the original serialisation for clients that read text.
assert json.loads(opportunity_team_result["result"]["content"][0]["text"])[0]["role"] == "CONSULTANT"
# Arguments are checked against the tool schema before anything runs: a name
# where a UUID belongs, a misspelled filter and a missing field all come back
# as a message the model can act on, never as a database error.
bad_id = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 34, "method": "tools/call", "params": {"name": "get_customer", "arguments": {"id": "Offline Verification 고객"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert bad_id["result"]["isError"] is True and "UUID" in bad_id["result"]["content"][0]["text"], bad_id
assert "SQLSTATE" not in bad_id["result"]["content"][0]["text"]
missing = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 35, "method": "tools/call", "params": {"name": "get_customer", "arguments": {}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert missing["result"]["isError"] is True and "필수 입력" in missing["result"]["content"][0]["text"], missing
renamed = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 36, "method": "tools/call", "params": {"name": "get_opportunity_team", "arguments": {"ID": opportunity["id"]}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert renamed["result"]["isError"] is False, renamed
unknown = request(
    "/mcp",
    "POST",
    {"jsonrpc": "2.0", "id": 37, "method": "tools/call", "params": {"name": "get_opportunity_team", "arguments": {"id": opportunity["id"], "owner": "me"}}},
    {**mcp_headers, "MCP-Protocol-Version": "2025-11-25"},
)
assert unknown["result"]["isError"] is True and "owner" in unknown["result"]["content"][0]["text"], unknown
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
# Restore the default before later VOC MCP coverage. An empty allowlist means
# every tool permitted by the key scopes is advertised.
request(
    "/api/v1/admin/settings/mcp/tool_allowlist",
    "PUT",
    {"value": [], "valueType": "json", "version": 2},
    csrf_header,
)
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
operations = request("/api/v1/admin/operations")
assert 0 <= operations["readinessScore"] <= 100
assert operations["secrets"]["registered"] is True and operations["secrets"]["matches"] is True
assert len(operations["secrets"]["keyId"]) == 12
diagnostic_keys = {item["key"] for item in operations["diagnostics"]}
assert {"postgresql", "schema", "master-key", "storage"}.issubset(diagnostic_keys)
assert operations["features"]["api"] is True and operations["features"]["mcp"] is True
support_bundle = request("/api/v1/admin/operations/support-bundle")
assert support_bundle["product"] == "Relio"
assert len(support_bundle["operations"]["diagnostics"]) >= 10
support_raw = json.dumps(support_bundle)
assert initial_password not in support_raw and key["secret"] not in support_raw

quality = request("/api/v1/admin/data-quality")
assert 0 <= quality["score"] <= 100 and quality["totalIssues"] >= 3
assert len(quality["categories"]) == 8
quality_by_key = {item["key"]: item for item in quality["categories"]}
assert quality_by_key["customer-registration"]["count"] >= 1
assert quality_by_key["contact-channel"]["count"] >= 3
assert quality_by_key["opportunity-next-action"]["count"] >= 1

configuration = request("/api/v1/admin/configuration/export")
assert configuration["format"] == "relio-config/v1" and configuration["product"] == "Relio"
assert len(configuration["pipelines"]) >= 1 and isinstance(configuration["settings"], list)
configuration_raw = json.dumps(configuration).lower()
assert initial_password.lower() not in configuration_raw
assert key["secret"].lower() not in configuration_raw
for forbidden in ('"clientsecret"', '"password"', '"dsn"'):
    assert forbidden not in configuration_raw
preview = request("/api/v1/admin/configuration/preview", "POST", configuration, csrf_header)
assert preview["safeToApply"] is True and preview["summary"]["total"] > 0
assert preview["summary"]["create"] == 0 and preview["summary"]["update"] == 0
applied = request(
    "/api/v1/admin/configuration/apply",
    "POST",
    {"confirmation": "APPLY", "bundle": configuration},
    csrf_header,
)
assert applied["applied"] is True and applied["preview"]["safeToApply"] is True

support_audit = request("/api/v1/admin/audit?channel=ADMIN&q=SUPPORT_BUNDLE&limit=10")
assert "SUPPORT_BUNDLE_EXPORT" in {item["action"] for item in support_audit["items"]}
configuration_audit = request("/api/v1/admin/audit?channel=ADMIN&q=CONFIGURATION_BUNDLE&limit=10")
configuration_actions = {item["action"] for item in configuration_audit["items"]}
assert {"CONFIGURATION_BUNDLE_EXPORT", "CONFIGURATION_BUNDLE_APPLY"}.issubset(configuration_actions)
# ---------------------------------------------------------------- 개인 작업공간
# A saved view stores an opaque querystring; a favorite stores a pointer. Neither
# may widen what its owner can see, so both are re-read through scoped queries.
view = request(
    "/api/v1/me/views",
    "POST",
    {"resource": "CUSTOMER", "name": "A등급 거래 고객", "query": "customerType=CUSTOMER&grade=A", "pinned": True},
    csrf_header,
)
assert view["query"] == "customerType=CUSTOMER&grade=A" and view["pinned"] is True
# Saving the same name again updates rather than duplicating.
again = request(
    "/api/v1/me/views",
    "POST",
    {"resource": "CUSTOMER", "name": "A등급 거래 고객", "query": "grade=A", "pinned": False},
    csrf_header,
)
assert again["id"] == view["id"] and again["query"] == "grade=A"
assert len([v for v in request("/api/v1/me/views?resource=CUSTOMER")["items"] if v["name"] == "A등급 거래 고객"]) == 1
expect_http_error("/api/v1/me/views", "POST", {"resource": "USERS", "name": "x", "query": ""}, csrf_header, contains="invalid resource")
expect_http_error("/api/v1/me/views", "POST", {"resource": "CUSTOMER", "name": "  ", "query": ""}, csrf_header, contains="name is required")
request(f"/api/v1/me/views/{view['id']}", "DELETE", None, csrf_header, expect_json=False)
assert not [v for v in request("/api/v1/me/views")["items"] if v["id"] == view["id"]]

toggled = request("/api/v1/me/favorites", "POST", {"resource": "CUSTOMER", "resourceId": customer["id"]}, csrf_header)
assert toggled["favorited"] is True
assert customer["id"] in request("/api/v1/me/favorites?resource=CUSTOMER")["ids"]
starred = request("/api/v1/me/favorites")["items"]
assert any(item["resourceId"] == customer["id"] and item["resource"] == "CUSTOMER" for item in starred)
# The same call unstars, so the star is a single idempotent control.
assert request("/api/v1/me/favorites", "POST", {"resource": "CUSTOMER", "resourceId": customer["id"]}, csrf_header)["favorited"] is False
# A record the caller cannot read must not be starrable, or favorites would
# confirm the existence of out-of-scope data.
expect_http_error(
    "/api/v1/me/favorites",
    "POST",
    {"resource": "CUSTOMER", "resourceId": "00000000-0000-4000-8000-000000000000"},
    csrf_header,
    contains="not found",
)

# The priority queue and the churn score must answer for the seeded data.
today = request("/api/v1/today")
assert isinstance(today["items"], list) and isinstance(today["risks"], list)
risk = request(f"/api/v1/customers/{customer['id']}/risk")
assert risk["customerId"] == customer["id"] and 0 <= risk["score"] <= 100
assert risk["level"] in {"HEALTHY", "WATCH", "HIGH", "CRITICAL"}

openapi = request("/api/openapi.json")
assert "get" in openapi["paths"]["/admin/data-quality"]
assert "post" in openapi["paths"]["/admin/configuration/apply"]
assert "delete" in openapi["paths"]["/admin/roles/{id}"]
assert "get" in openapi["paths"]["/today"]
assert "post" in openapi["paths"]["/me/favorites"]
assert "put" in openapi["paths"]["/admin/stages/{id}"]

# ---------------------------------------------------------------- admin CRUD
# Every administrator resource that can be listed must also be editable and
# removable, and destructive calls must refuse to break referential integrity.
permission_catalog = request("/api/v1/admin/permissions")
assert {"permission", "label", "group"} <= set(permission_catalog["items"][0])
assert "opportunity:read" in {item["permission"] for item in permission_catalog["items"]}
assert {"USER", "TEAM", "COMPANY"} <= {item["value"] for item in permission_catalog["dataScopes"]}

roles = request("/api/v1/admin/roles")["items"]
default_roles = [role for role in roles if role["isDefault"]]
assert len(default_roles) == 1, "exactly one Role must be the SSO sign-in default"
assert "opportunity:read" in default_roles[0]["permissions"], "the default Role must be able to open the dashboard"
system_role = next(role for role in roles if role["systemRole"])
expect_http_error(f"/api/v1/admin/roles/{system_role['id']}", "DELETE", headers=csrf_header, contains="system Role")

created_role = request(
    "/api/v1/admin/roles",
    "POST",
    {
        "code": "SMOKE_TEMP_ROLE",
        "name": "Smoke 임시 Role",
        "description": "삭제 검증용",
        "dataScope": "USER",
        "permissions": ["customer:read", "opportunity:read"],
    },
    csrf_header,
)
request(
    f"/api/v1/admin/roles/{created_role['id']}",
    "PUT",
    {
        "name": "Smoke 임시 Role 2",
        "description": "수정 검증용",
        "dataScope": "TEAM",
        "permissions": ["customer:read"],
        "isDefault": False,
    },
    csrf_header,
)
updated_role = next(role for role in request("/api/v1/admin/roles")["items"] if role["id"] == created_role["id"])
assert updated_role["dataScope"] == "TEAM" and updated_role["permissions"] == ["customer:read"]
expect_http_error(
    f"/api/v1/admin/roles/{created_role['id']}",
    "PUT",
    {"name": "typo", "dataScope": "USER", "permissions": ["customer:reed"], "isDefault": False},
    csrf_header,
    contains="unknown permission",
)
request(f"/api/v1/admin/roles/{created_role['id']}", "DELETE", None, csrf_header, expect_json=False)
assert created_role["id"] not in {role["id"] for role in request("/api/v1/admin/roles")["items"]}

created_org = request(
    "/api/v1/admin/organizations",
    "POST",
    {"name": "Smoke 임시 조직", "code": "SMOKE_TEMP_ORG", "type": "TEAM"},
    csrf_header,
)
request(
    f"/api/v1/admin/organizations/{created_org['id']}",
    "PUT",
    {"name": "Smoke 임시 조직 2", "code": "SMOKE_TEMP_ORG", "type": "DEPARTMENT", "parentId": "", "active": True},
    csrf_header,
)
assert "Smoke 임시 조직 2" in {org["name"] for org in request("/api/v1/admin/organizations")["items"]}
request(f"/api/v1/admin/organizations/{created_org['id']}", "DELETE", None, csrf_header, expect_json=False)
root_org = next(org for org in request("/api/v1/admin/organizations")["items"] if org["code"] == "RELIO")
expect_http_error(f"/api/v1/admin/organizations/{root_org['id']}", "DELETE", headers=csrf_header, contains="RELIO")

custom_field = request(
    "/api/v1/admin/custom-fields",
    "POST",
    {"entityType": "Customer", "key": "smoke_temp_field", "label": "Smoke Field", "type": "Text", "required": False, "displayOrder": 900},
    csrf_header,
)
request(
    f"/api/v1/admin/custom-fields/{custom_field['id']}",
    "PUT",
    {"label": "Smoke Field 2", "type": "Select", "required": True, "options": ["A", "B"], "active": False, "displayOrder": 910},
    csrf_header,
)
edited_field = next(item for item in request("/api/v1/admin/custom-fields")["items"] if item["id"] == custom_field["id"])
assert edited_field["label"] == "Smoke Field 2" and edited_field["type"] == "Select" and edited_field["active"] is False
request(f"/api/v1/admin/custom-fields/{custom_field['id']}", "DELETE", None, csrf_header)

admin_pipelines = request("/api/v1/admin/pipelines")["items"]
default_pipeline = next(pipeline for pipeline in admin_pipelines if pipeline["default"])
expect_http_error(f"/api/v1/admin/pipelines/{default_pipeline['id']}", "DELETE", headers=csrf_header, contains="default Pipeline")
new_stage = request(
    f"/api/v1/admin/pipelines/{default_pipeline['id']}/stages",
    "POST",
    {
        "name": "Smoke 임시 Stage",
        "order": max(stage["order"] for stage in default_pipeline["stages"]) + 1,
        "probability": 15,
        "forecastCategory": "PIPELINE",
        "isWon": False,
        "isLost": False,
        "color": "#888888",
    },
    csrf_header,
)
request(
    f"/api/v1/admin/stages/{new_stage['id']}",
    "PUT",
    {
        "name": "Smoke 임시 Stage 2",
        "order": 2,
        "probability": 25,
        "forecastCategory": "BEST_CASE",
        "isWon": False,
        "isLost": False,
        "active": True,
        "color": "#777777",
        "minDays": None,
        "maxDays": 14,
    },
    csrf_header,
)
reordered = next(p for p in request("/api/v1/admin/pipelines")["items"] if p["id"] == default_pipeline["id"])
moved = next(stage for stage in reordered["stages"] if stage["id"] == new_stage["id"])
assert moved["order"] == 2 and moved["probability"] == 25 and moved["forecastCategory"] == "BEST_CASE"
orders = sorted(stage["order"] for stage in reordered["stages"])
assert orders == list(range(1, len(orders) + 1)), f"stage order must stay contiguous: {orders}"
request(f"/api/v1/admin/stages/{new_stage['id']}", "DELETE", None, csrf_header, expect_json=False)
compacted = next(p for p in request("/api/v1/admin/pipelines")["items"] if p["id"] == default_pipeline["id"])
orders = sorted(stage["order"] for stage in compacted["stages"])
assert orders == list(range(1, len(orders) + 1)), f"stage order must stay contiguous after delete: {orders}"

smoke_product = request(
    "/api/v1/products",
    "POST",
    {"code": "SMOKE-TEMP", "name": "Smoke 임시 상품", "description": "삭제 검증용", "unitPrice": 1000},
    csrf_header,
)
request(
    f"/api/v1/products/{smoke_product['id']}",
    "PUT",
    {"code": "SMOKE-TEMP", "name": "Smoke 임시 상품 2", "description": "수정 검증용", "unitPrice": 2000, "active": False},
    csrf_header,
)
assert any(
    item["name"] == "Smoke 임시 상품 2" and item["active"] is False
    for item in request("/api/v1/products?limit=200")["items"]
)
request(f"/api/v1/products/{smoke_product['id']}", "DELETE", None, csrf_header, expect_json=False)

# A managed user must be editable, re-rolable and deactivatable, and the
# Break Glass account must resist all three.
smoke_user = request(
    "/api/v1/admin/users",
    "POST",
    {
        "username": "smoke-managed-user",
        "displayName": "Smoke 관리 대상",
        "email": "smoke-managed@relio.invalid",
        "password": "Smoke-Managed-Password-2026",
        "organizationId": "",
        "title": "사원",
        "roleIds": [],
    },
    csrf_header,
)
request(
    f"/api/v1/admin/users/{smoke_user['id']}",
    "PUT",
    {"displayName": "Smoke 관리 대상 2", "email": "smoke-managed@relio.invalid", "organizationId": "", "managerId": "", "title": "대리", "active": True},
    csrf_header,
)
request(
    f"/api/v1/admin/users/{smoke_user['id']}/roles",
    "PUT",
    {"roleIds": [default_roles[0]["id"]]},
    csrf_header,
)
request(
    f"/api/v1/admin/users/{smoke_user['id']}/password",
    "POST",
    {"password": "Smoke-Reset-Password-2026"},
    csrf_header,
)
managed = next(item for item in request("/api/v1/admin/users")["items"] if item["id"] == smoke_user["id"])
assert managed["displayName"] == "Smoke 관리 대상 2" and managed["title"] == "대리"
assert managed["roles"] == [default_roles[0]["name"]]
bootstrap_user = next(item for item in request("/api/v1/admin/users")["items"] if item["isBootstrap"])
# This smoke test is signed in as the Bootstrap administrator, so deleting that
# account trips the self-deactivation guard before the break-glass guard. Assert
# each guard through the call that actually reaches it.
expect_http_error(
    f"/api/v1/admin/users/{bootstrap_user['id']}",
    "DELETE",
    headers=csrf_header,
    contains="their own account",
)
expect_http_error(
    f"/api/v1/admin/users/{bootstrap_user['id']}",
    "PUT",
    {
        "displayName": bootstrap_user["displayName"],
        "email": bootstrap_user["email"],
        "organizationId": bootstrap_user["organizationId"],
        "managerId": bootstrap_user["managerId"],
        "title": bootstrap_user["title"],
        "active": False,
    },
    csrf_header,
    contains="break glass",
)
request(f"/api/v1/admin/users/{smoke_user['id']}", "DELETE", None, csrf_header)
assert not next(item for item in request("/api/v1/admin/users")["items"] if item["id"] == smoke_user["id"])["active"]

# Deleting a setting restores the compiled-in default.
request("/api/v1/admin/settings/system/locale", "PUT", {"value": "en-US", "valueType": "string"}, csrf_header)
request("/api/v1/admin/settings/system/locale", "DELETE", None, csrf_header)
assert "locale" not in {item["key"] for item in request("/api/v1/admin/settings?namespace=system")["items"]}

# Default (workspace-less) VOC fields must be discoverable before a strict
# gateway will even send tools/call. These are disposable test settings only.
voice_key = request(
    "/api/v1/me/keys", "POST",
    {"name": "Offline VOC MCP", "scopes": ["mcp:use", "customer:read", "customer:write", "voice:read", "voice:write"], "channels": ["MCP"]},
    csrf_header,
)
voice_mcp_headers = {
    "Authorization": "Bearer " + voice_key["secret"],
    "Accept": "application/json, text/event-stream", "Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25",
}


def voice_tool_catalog(headers=voice_mcp_headers):
    response = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 180, "method": "tools/list"}, headers)
    assert "error" not in response, response
    return {item["name"]: item for item in response["result"]["tools"]}


def voice_tool_call(name, arguments, *, validate=True, headers=voice_mcp_headers, catalog=None):
    if validate:
        advertised = catalog if catalog is not None else voice_tool_catalog(headers)
        assert_mcp_schema(advertised[name]["inputSchema"], arguments)
    return request("/mcp", "POST", {
        "jsonrpc": "2.0", "id": 181, "method": "tools/call", "params": {"name": name, "arguments": arguments},
    }, headers)["result"]


before_fields = voice_tool_catalog()
assert not request("/api/v1/voices/workspaces")["items"]
global_definitions = [
    {"key": "service_type", "label": "서비스 구분", "type": "Select", "phase": "INTAKE", "required": True, "options": ["PHONE"]},
    {"key": "dev_method", "label": "개발방식", "type": "Select", "phase": "INTAKE", "options": ["POPUP_MOD"]},
    {"key": "error_code", "label": "오류코드", "type": "Text", "phase": "INTAKE"},
    {"key": "resolver", "label": "해결 주체", "type": "Select", "phase": "RESOLUTION", "options": ["GLOBAL_OPS"]},
]
global_field_ids = {}
for definition in global_definitions:
    created = request("/api/v1/admin/custom-fields", "POST", {"entityType": "VOICE", **definition}, csrf_header)
    global_field_ids[definition["key"]] = created["id"]
global_tools = voice_tool_catalog()
global_intake_schema = global_tools["file_customer_voice"]["inputSchema"]["properties"]["fields"]
assert set(global_intake_schema["properties"]) == {"service_type", "dev_method", "error_code"}, global_intake_schema
assert global_intake_schema["additionalProperties"] is False
assert "service_type" not in before_fields["file_customer_voice"]["inputSchema"]["properties"]["fields"]["properties"]
assert set(global_tools["resolve_customer_voice"]["inputSchema"]["properties"]["fields"]["properties"]) == {"resolver"}
discovered = voice_tool_call("get_voice_categories", {})["structuredContent"]
assert {field["key"] for field in discovered["fields"]} == set(global_field_ids) and not discovered["workspaces"], discovered
default_customer = request("/api/v1/customers", "POST", {"name": "MCP 기본 민원 스키마 검증"}, csrf_header)
global_values = {"service_type": "PHONE", "dev_method": "POPUP_MOD", "error_code": "R3001"}
global_args = {"customerId": default_customer["id"], "voiceType": "INQUIRY", "title": "MCP R3001", "fields": global_values}
# This reproduces the deadlock: REST requires service_type, and now the MCP
# input schema actually permits the very same key and approved value.
rest_case = request("/api/v1/voices", "POST", {
    "customerId": default_customer["id"], "voiceType": "INQUIRY", "title": "REST R3001", "customFields": global_values,
}, csrf_header)
filed = voice_tool_call("file_customer_voice", global_args, catalog=global_tools)
assert filed["isError"] is False, filed
global_case = filed["structuredContent"]
stored = request(f"/api/v1/voices/{global_case['id']}")["voice"]
assert stored["customFields"] == rest_case["customFields"] == global_values, stored
missing = voice_tool_call("file_customer_voice", {k: v for k, v in global_args.items() if k != "fields"})
assert missing["isError"] and "서비스 구분" in missing["content"][0]["text"], missing
for bad_fields in (
    {**global_values, "service_type": "INVALID"},
    {**global_values, "unknown_key": "x"},
    {"serviceType": "PHONE"},
    {global_field_ids["service_type"]: "PHONE"},
):
    try:
        assert_mcp_schema(global_intake_schema, bad_fields)
    except AssertionError:
        pass
    else:
        raise AssertionError(f"gateway must reject invalid fields: {bad_fields}")
    rejected = voice_tool_call("file_customer_voice", {**global_args, "fields": bad_fields}, validate=False)
    assert rejected["isError"], rejected
resolved = voice_tool_call("resolve_customer_voice", {
    "id": global_case["id"], "resolution": "승인한 해결 내용", "causeEvidence": "CONFIRMED", "fields": {"resolver": "GLOBAL_OPS"},
})
assert resolved["isError"] is False and resolved["structuredContent"]["customFields"]["resolver"] == "GLOBAL_OPS", resolved
request(f"/api/v1/voices/{global_case['id']}/knowledge", "PUT", {"knowledgeStatus": "APPROVED"}, csrf_header)
found = voice_tool_call("search_voice_knowledge", {"query": "R3001", "fields": {"error_code": "R3001"}})
assert found["isError"] is False and [item["id"] for item in found["structuredContent"]["items"]] == [global_case["id"]], found
# Configuration changes must be visible on the next tools/list, without a
# server restart. Inactive definitions must disappear as well.
request(f"/api/v1/admin/custom-fields/{global_field_ids['service_type']}", "PUT", {
    **{k: v for k, v in global_definitions[0].items() if k != "key"}, "options": ["PHONE", "SMS"],
}, csrf_header)
refreshed = voice_tool_catalog()
assert refreshed["file_customer_voice"]["inputSchema"]["properties"]["fields"]["properties"]["service_type"]["enum"] == ["PHONE", "SMS"]
request(f"/api/v1/admin/custom-fields/{global_field_ids['dev_method']}", "PUT", {
    **{k: v for k, v in global_definitions[1].items() if k != "key"}, "active": False,
}, csrf_header)
assert "dev_method" not in voice_tool_catalog()["file_customer_voice"]["inputSchema"]["properties"]["fields"]["properties"]
request(f"/api/v1/admin/custom-fields/{global_field_ids['dev_method']}", "PUT", {
    **{k: v for k, v in global_definitions[1].items() if k != "key"}, "active": True,
}, csrf_header)
print("MCP default VOC schemas, canonical keys, REST parity, validation and refresh passed")

# VOC workspaces: a department preset, its SLA-free types and fields, quick
# member registration, the structured resolution and the knowledge gate.
department = request("/api/v1/admin/organizations", "POST", {"name": "Offline 회원사 지원부", "code": "OFFLINE_MS", "type": "DEPARTMENT"}, csrf_header)
voice_agent = request(
    "/api/v1/admin/users",
    "POST",
    {
        "username": "offline-voice-agent",
        "displayName": "Offline 민원 담당자",
        "email": "offline-voice-agent@relio.invalid",
        "password": "Offline-Agent-Password-2026!",
        "organizationId": department["id"],
        "title": "담당자",
        "roleIds": [],
    },
    csrf_header,
)
voice_reviewer = request(
    "/api/v1/admin/users",
    "POST",
    {
        "username": "offline-voice-reviewer",
        "displayName": "Offline 민원 검토자",
        "email": "offline-voice-reviewer@relio.invalid",
        "password": "Offline-Reviewer-Password-2026!",
        "organizationId": department["id"],
        "title": "검토자",
        "roleIds": [],
    },
    csrf_header,
)
preset_config = {
    "organizationId": department["id"], "customerCodePattern": "^[0-9]{12}$", "customerCodeUniqueScope": "GLOBAL",
    "categories": [
        {"code": "MS_SERVICE_ERROR", "name": "서비스 오류 신고", "voiceType": "DEFECT"},
        {"code": "MS_SMS_ISSUE", "name": "인증문자 이상", "voiceType": "DEFECT"},
        {"code": "MS_INTEGRATION", "name": "연동·개발 문의", "voiceType": "INQUIRY"},
        {"code": "MS_SETTING_CHANGE", "name": "설정·정보 변경 요청", "voiceType": "REQUEST"},
        {"code": "MS_SECURITY_DOCS", "name": "위수탁·보안 자료 요청", "voiceType": "REQUEST"},
        {"code": "MS_BILLING", "name": "요금·정산 문의", "voiceType": "INQUIRY"},
        {"code": "MS_ADMIN_SITE", "name": "관리자 사이트 문의", "voiceType": "INQUIRY"},
        {"code": "MS_OTHER", "name": "기타", "voiceType": "INQUIRY"},
    ],
    "serviceType": {"required": True, "defaultValue": "", "values": [{"code": "IPIN", "name": "아이핀"}, {"code": "PHONE", "name": "휴대폰본인확인"}]},
    "developmentMethod": {"required": False, "defaultValue": "UNKNOWN", "values": [{"code": "DIRECT", "name": "직접개발"}, {"code": "UNKNOWN", "name": "미확인"}]},
    "errorCode": {"required": False, "defaultValue": "", "values": []},
    "resolver": {"required": False, "defaultValue": "", "values": [{"code": "MEMBER", "name": "회원사 조치"}, {"code": "OPS", "name": "운영 조치"}, {"code": "IT", "name": "IT 조치"}, {"code": "GUIDE", "name": "안내 종결"}]},
    "agentUserIds": [], "reviewerUserIds": [],
}
preset = request("/api/v1/admin/voice-presets/MEMBER_SUPPORT/apply", "POST", preset_config, csrf_header)
assert len(preset["created"]) == 15 and not preset["skipped"], preset
assert len(preset["pending"]) == 2, preset
assigned_config = {
    **preset_config,
    "agentUserIds": [voice_agent["id"]],
    "reviewerUserIds": [voice_reviewer["id"]],
}
again = request("/api/v1/admin/voice-presets/MEMBER_SUPPORT/apply", "POST", assigned_config, csrf_header)
assert len(again["created"]) == 2 and len(again["skipped"]) == 15 and not again["pending"], again
member_ws = next(w for w in request("/api/v1/voices/workspaces")["items"] if w["code"] == "MEMBER_SUPPORT")
assert len(member_ws["categories"]) == 8 and not member_ws["slaEnabled"] and member_ws["isolated"] and member_ws["knowledgeGate"]
agent_role = next(role for role in request("/api/v1/admin/roles")["items"] if role["code"] == "MEMBER_SUPPORT_AGENT")
other_department = request("/api/v1/admin/organizations", "POST", {"name": "Offline 타 부서", "code": "OFFLINE_OTHER", "type": "DEPARTMENT"}, csrf_header)
other_agent = request(
    "/api/v1/admin/users",
    "POST",
    {
        "username": "offline-other-agent",
        "displayName": "Offline 타 부서 담당자",
        "email": "offline-other-agent@relio.invalid",
        "password": "Offline-Other-Password-2026!",
        "organizationId": other_department["id"],
        "title": "담당자",
        "roleIds": [agent_role["id"]],
    },
    csrf_header,
)
expect_http_error(f"/api/v1/voices/workspaces/{member_ws['id']}/customers", "POST", {"name": "코드 오류", "customerCode": "123"}, csrf_header, contains="회원사코드")
member = request(f"/api/v1/voices/workspaces/{member_ws['id']}/customers", "POST", {"name": "Offline 회원사", "customerCode": "100000000001"}, csrf_header)
assert member["customerCode"] == "100000000001"
member_search = request(f"/api/v1/voices/workspaces/{member_ws['id']}/customers?q=100000000001&limit=20")["items"]
assert member_search == [{"id": member["id"], "customerId": member["id"], "name": "Offline 회원사", "customerCode": "100000000001"}], member_search
expect_http_error(f"/api/v1/voices/workspaces/{member_ws['id']}/customers", "POST", {"name": "중복", "customerCode": "100000000001"}, csrf_header, contains="Offline 회원사")
same_name_member = request(f"/api/v1/voices/workspaces/{member_ws['id']}/customers", "POST", {"name": "Offline 회원사", "customerCode": "100000000009"}, csrf_header)
name_matches = request(f"/api/v1/voices/workspaces/{member_ws['id']}/customers?q={urllib.parse.quote('Offline 회원사')}&limit=20")["items"]
assert {item["customerId"] for item in name_matches} == {member["id"], same_name_member["id"]}, name_matches
error_type = next(c for c in member_ws["categories"] if c["code"] == "MS_SERVICE_ERROR")
intake = {"customerId": member["id"], "categoryId": error_type["id"], "title": "Offline B124", "body": "B124 발생", "severity": "CRITICAL"}
general_customer = request("/api/v1/customers", "POST", {"name": "영역 밖 고객"}, csrf_header)
expect_http_error("/api/v1/voices", "POST", {**intake, "customerId": general_customer["id"], "customFields": {"service_type": "IPIN"}}, csrf_header, contains="업무 영역에 등록된 고객")
expect_http_error("/api/v1/voices", "POST", {**intake, "customFields": {}}, csrf_header, contains="서비스 구분")
case = request("/api/v1/voices", "POST", {**intake, "customFields": {"service_type": "IPIN", "dev_method": "UNKNOWN", "error_code": "B124"}}, csrf_header)
assert case["slaApplied"] is False and not case.get("responseDueAt") and case["knowledgeStatus"] == "UNREVIEWED"
# Manual phase-specific editing: optional fields clear, defaults are not
# silently restored, required fields cannot be cleared, and reload agrees.
case = request(f"/api/v1/voices/{case['id']}", "PUT", {"customFields": {"error_code": None, "dev_method": None}, "version": case["version"]}, csrf_header)
assert case["customFields"].get("error_code") is None and case["customFields"].get("dev_method") is None, case
reloaded = request(f"/api/v1/voices/{case['id']}")["voice"]
assert reloaded["customFields"].get("error_code") is None and reloaded["customFields"].get("dev_method") is None, reloaded
expect_http_error(f"/api/v1/voices/{case['id']}", "PUT", {"customFields": {"service_type": None}, "version": case["version"]}, csrf_header, contains="서비스 구분")
case = request(f"/api/v1/voices/{case['id']}", "PUT", {"customFields": {"error_code": "B124"}, "version": case["version"]}, csrf_header)
assert case["customFields"]["service_type"] == "IPIN" and case["customFields"].get("dev_method") is None, case
other_call, other_headers = authenticated_client(
    "offline-other-agent",
    "Offline-Other-Password-2026!",
    "Offline-Other-Changed-Password-2026!",
)
assert member_ws["id"] not in {w["id"] for w in other_call("/api/v1/voices/workspaces")["items"]}
expect_client_error(other_call, f"/api/v1/voices/{case['id']}")
expect_client_error(other_call, f"/api/v1/customers/{member['id']}")
expect_client_error(other_call, f"/api/v1/voices/workspaces/{member_ws['id']}/customers?q=100000000001")
case = request(f"/api/v1/voices/{case['id']}", "PUT", {"status": "IN_PROGRESS", "version": case["version"]}, csrf_header)
expect_http_error(f"/api/v1/voices/{case['id']}", "PUT", {"status": "RESOLVED", "resolution": "안내", "version": case["version"]}, csrf_header, contains="원인 근거")
expect_http_error(f"/api/v1/voices/{case['id']}", "PUT", {"status": "RESOLVED", "resolution": "안내", "causeEvidence": "CONFIRMED", "customFields": {"resolver": "OUTSIDE"}, "version": case["version"]}, csrf_header, contains="해결 주체")
case = request(f"/api/v1/voices/{case['id']}", "PUT", {"status": "RESOLVED", "resolution": "리턴 URL 재등록", "causeEvidence": "CONFIRMED", "customFields": {"resolver": "MEMBER"}, "version": case["version"]}, csrf_header)
assert case["status"] == "RESOLVED" and case["causeEvidence"] == "CONFIRMED"
assert request("/api/v1/voices/knowledge?q=B124")["items"] == []
assert request(f"/api/v1/voices/summary?workspaceId={member_ws['id']}")["knowledgePending"] == 1
agent_call, agent_headers = authenticated_client(
    "offline-voice-agent",
    "Offline-Agent-Password-2026!",
    "Offline-Agent-Changed-Password-2026!",
)
assert agent_call(f"/api/v1/voices/{case['id']}")["voice"]["id"] == case["id"]
try:
    agent_call(
        f"/api/v1/voices/{case['id']}/knowledge",
        "PUT",
        {"knowledgeStatus": "APPROVED", "note": "담당자 직접 API 시도"},
        agent_headers,
    )
except RuntimeError as exc:
    assert "voice:knowledge-review" in str(exc), str(exc)
else:
    raise AssertionError("a regular voice agent changed the knowledge review status")
reviewer_call, reviewer_headers = authenticated_client(
    "offline-voice-reviewer",
    "Offline-Reviewer-Password-2026!",
    "Offline-Reviewer-Changed-Password-2026!",
)
case = reviewer_call(
    f"/api/v1/voices/{case['id']}/knowledge",
    "PUT",
    {"knowledgeStatus": "APPROVED", "note": "offline reviewer"},
    reviewer_headers,
)
assert case["knowledgeStatus"] == "APPROVED"
found = request("/api/v1/voices/knowledge?q=B124")["items"]
assert [x["voiceNo"] for x in found] == [case["voiceNo"]] and found[0]["fields"]["resolver"] == "MEMBER"
field_filter = urllib.parse.quote(json.dumps({"error_code": "B124"}, separators=(",", ":")))
found_by_field = request(f"/api/v1/voices/knowledge?workspaceId={member_ws['id']}&fieldFilters={field_filter}")["items"]
assert [x["voiceNo"] for x in found_by_field] == [case["voiceNo"]], found_by_field
assert len(request("/api/v1/voices/history?customerCode=100000000001")["items"]) == 1
reopened = request(f"/api/v1/voices/{case['id']}", "PUT", {"status": "IN_PROGRESS", "version": case["version"]}, csrf_header)
assert reopened["knowledgeStatus"] == "UNREVIEWED" and request("/api/v1/voices/knowledge?q=B124")["items"] == []
imported = request(f"/api/v1/voices/workspaces/{member_ws['id']}/customers/import", "POST", {"rows": [{"name": "일괄 1", "customerCode": "100000000002"}, {"name": "기존", "customerCode": "100000000001"}, {"name": "오류", "customerCode": "x"}]}, csrf_header)
assert (imported["created"], imported["unchanged"], imported["failed"]) == (1, 1, 1), imported

# The same workflow through MCP proves strict schemas remain parseable by
# Qwen/OpenCode-style JSON-RPC clients and that the advertised customerId can
# be passed directly to intake. The values below are the approved preview.
voice_tools = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 80, "method": "tools/list", "params": {}}, voice_mcp_headers)
voice_tool_map = {tool["name"]: tool for tool in voice_tools["result"]["tools"]}
assert {"search_workspace_customers", "file_customer_voice", "record_voice_response", "resolve_customer_voice"}.issubset(voice_tool_map)
assert voice_tool_map["file_customer_voice"]["annotations"]["readOnlyHint"] is False
assert_mcp_schema(voice_tool_map["file_customer_voice"]["inputSchema"], global_args)
assert_mcp_schema(voice_tool_map["file_customer_voice"]["inputSchema"], {
    "customerId": member["id"], "category": "MS_SERVICE_ERROR", "title": "MCP B124", "fields": {"service_type": "IPIN", "error_code": "B124"},
})
# Other departments must not learn isolated field definitions or enum values.
other_key = other_call("/api/v1/me/keys", "POST", {
    "name": "Offline Other VOC", "scopes": ["mcp:use", "customer:read", "voice:read", "voice:write"], "channels": ["MCP"],
}, other_headers)
other_mcp_headers = {**voice_mcp_headers, "Authorization": "Bearer " + other_key["secret"]}
other_tools = voice_tool_catalog(other_mcp_headers)
other_service = other_tools["file_customer_voice"]["inputSchema"]["properties"]["fields"]["properties"]["service_type"]
assert other_service["enum"] == ["PHONE", "SMS"] and "anyOf" not in other_service, other_service
other_discovery = voice_tool_call("get_voice_categories", {}, headers=other_mcp_headers)["structuredContent"]
assert member_ws["id"] not in {w["id"] for w in other_discovery["workspaces"]}
cross_workspace = voice_tool_call("file_customer_voice", {
    "customerId": member["id"], "category": error_type["id"], "title": "거부되어야 함", "fields": {"service_type": "PHONE"},
}, headers=other_mcp_headers)
assert cross_workspace["isError"], cross_workspace
searched = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 81, "method": "tools/call", "params": {
    "name": "search_workspace_customers", "arguments": {"workspaceId": member_ws["id"], "query": "100000000001"},
}}, voice_mcp_headers)["result"]
assert searched["isError"] is False and searched["structuredContent"]["items"][0]["customerId"] == member["id"], searched
mcp_case_result = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 82, "method": "tools/call", "params": {
    "name": "file_customer_voice", "arguments": {"customerId": member["id"], "category": "MS_SERVICE_ERROR", "title": "MCP B124", "body": "B124 재현", "fields": {"service_type": "IPIN", "error_code": "B124"}},
}}, voice_mcp_headers)["result"]
assert mcp_case_result["isError"] is False, mcp_case_result
mcp_case = mcp_case_result["structuredContent"]
assert mcp_case["customFields"]["dev_method"] == "UNKNOWN" and not mcp_case.get("responseDueAt"), mcp_case
recorded = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 83, "method": "tools/call", "params": {
    "name": "record_voice_response", "arguments": {"id": mcp_case["id"], "eventType": "CUSTOMER_CONTACT", "note": "담당자 승인 응대"},
}}, voice_mcp_headers)["result"]
assert recorded["isError"] is False, recorded
resolved = request("/mcp", "POST", {"jsonrpc": "2.0", "id": 84, "method": "tools/call", "params": {
    "name": "resolve_customer_voice", "arguments": {"id": mcp_case["id"], "resolution": "설정 재적용", "causeEvidence": "UNIDENTIFIED", "fields": {"resolver": "MEMBER"}, "note": "담당자 승인 해결"},
}}, voice_mcp_headers)["result"]
assert resolved["isError"] is False and resolved["structuredContent"]["status"] == "RESOLVED", resolved
# An isolated workspace's requests and members never feed the sales signals.
request("/api/v1/intelligence/run", "POST", {}, csrf_header)
assert not request(f"/api/v1/customers/{member['id']}/intelligence").get("signals")

# Activity and customer list filters must actually narrow the result set.
filtered_customers = request("/api/v1/customers?customerType=PROSPECT&limit=200")["items"]
assert all(item["customerType"] == "PROSPECT" for item in filtered_customers)
filtered_activities = request("/api/v1/activities?type=MEETING&limit=100")["items"]
assert all(item["activityType"] == "MEETING" for item in filtered_activities)
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
