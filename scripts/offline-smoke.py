#!/usr/bin/env python3
import http.cookiejar
import json
import re
import sys
import urllib.error
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
key = request(
    "/api/v1/me/keys",
    "POST",
    {
        "name": "Offline MCP Verification",
        "scopes": ["mcp:use", "customer:read", "contact:read", "opportunity:read", "activity:read", "forecast:read", "approval:request", "approval:approve"],
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
assert {"get_account_brief", "get_account_relationships", "get_account_plan", "find_cross_sell_opportunities", "get_opportunity_team"}.issubset(tool_names)
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
assert opportunity_team_result["result"]["structuredContent"][0]["role"] == "CONSULTANT"
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
openapi = request("/api/openapi.json")
assert "get" in openapi["paths"]["/admin/data-quality"]
assert "post" in openapi["paths"]["/admin/configuration/apply"]
assert "delete" in openapi["paths"]["/admin/roles/{id}"]
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
