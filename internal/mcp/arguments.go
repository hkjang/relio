package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Arguments come from a language model, not from a form. Before this check the
// handlers read them with type assertions that quietly fell back: a limit sent
// as "5" became the default, a filter spelled customer_id was ignored and the
// tool answered with every record, and a customer name passed as an id reached
// PostgreSQL and came back as "invalid input syntax for type uuid". Each of
// those looked like a working call with a wrong answer. Validating against the
// tool's own input schema turns them into a message the model can act on.

// argumentError is a tools/call the model can fix by changing its arguments.
// It is reported as a tool result with isError rather than a JSON-RPC error:
// the 2025-11-25 revision asks for input validation failures to reach the model
// so it can correct itself, where a protocol error usually ends the turn.
type argumentError struct{ message string }

func (e *argumentError) Error() string { return e.message }

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isIDField reports a parameter that holds a record identifier. Every one of
// them is a UUID column, so the format can be checked before the query runs.
func isIDField(name string) bool {
	return name == "id" || (len(name) > 2 && strings.HasSuffix(name, "Id"))
}

// idLookupHint names the tool that finds an identifier the model only knows by
// name. Without it the model tends to retry the same call with the same name.
var idLookupHint = map[string]string{
	"customerId":    "search_customers",
	"contactId":     "search_contacts",
	"opportunityId": "list_opportunities",
	"contractId":    "get_contracts",
}

// normalizeName folds the spellings a model uses for the same parameter:
// customer_id, customer-id, CustomerID and customerId all become customerid.
func normalizeName(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(name))
}

// validateArguments checks args against a tool input schema and returns the
// arguments the handler should see: null values dropped, unambiguous spelling
// variants renamed, and scalar types a model commonly gets wrong converted.
func validateArguments(schema map[string]any, args map[string]any) (map[string]any, error) {
	properties, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(args))
	for key, value := range args {
		// A model sends null for "I have no value" far more often than it means
		// a literal null, and no tool here distinguishes the two.
		if value == nil {
			continue
		}
		out[key] = value
	}

	if open, isBool := schema["additionalProperties"].(bool); isBool && !open {
		// additionalProperties is false: anything unknown is either a typo to
		// repair or an argument to refuse. Silently ignoring it is the worst of
		// the three, because the call succeeds with the filter missing.
		byNormal := map[string]string{}
		for name := range properties {
			byNormal[normalizeName(name)] = name
		}
		unknown := []string{}
		for key, value := range out {
			if _, known := properties[key]; known {
				continue
			}
			target, ok := byNormal[normalizeName(key)]
			if _, taken := out[target]; ok && !taken {
				out[target] = value
				delete(out, key)
				continue
			}
			unknown = append(unknown, key)
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, &argumentError{fmt.Sprintf("알 수 없는 입력입니다: %s. 사용할 수 있는 입력: %s",
				strings.Join(unknown, ", "), strings.Join(sortedKeys(properties), ", "))}
		}
	}

	problems := []string{}
	for key, value := range out {
		property, _ := properties[key].(map[string]any)
		if property == nil {
			continue
		}
		converted, problem := convertArgument(key, property, value)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		if converted == nil {
			delete(out, key)
			continue
		}
		out[key] = converted
	}

	missing := []string{}
	for _, name := range requiredNames(schema) {
		value, ok := out[name]
		if text, isText := value.(string); isText && strings.TrimSpace(text) == "" {
			// A required name or title sent as "" is as missing as no value.
			ok = false
		}
		if !ok {
			label := name
			if property, _ := properties[name].(map[string]any); property != nil {
				if description, _ := property["description"].(string); description != "" {
					label = fmt.Sprintf("%s(%s)", name, description)
				}
			}
			missing = append(missing, label)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		problems = append(problems, "필수 입력이 빠졌습니다: "+strings.Join(missing, ", "))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, &argumentError{strings.Join(problems, " / ")}
	}
	return out, nil
}

// convertArgument returns the value the handler should receive, nil when the
// value means "not provided", or a problem sentence for the model.
func convertArgument(key string, property map[string]any, value any) (any, string) {
	kind, _ := property["type"].(string)
	switch kind {
	case "string":
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case float64:
			// A registration or phone number sent as a JSON number.
			text = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			text = strconv.FormatBool(v)
		default:
			return nil, fmt.Sprintf("%s은(는) 문자열이어야 합니다", key)
		}
		if isIDField(key) {
			text = strings.TrimSpace(text)
			if text == "" {
				// "비우면 전체" filters are optional ids; empty means absent.
				return nil, ""
			}
			if !uuidPattern.MatchString(text) {
				hint := "목록·검색 도구로 먼저 ID를 확인하세요."
				if tool, ok := idLookupHint[key]; ok {
					hint = fmt.Sprintf("이름만 알고 있다면 %s로 먼저 ID를 찾으세요.", tool)
				} else if key == "id" {
					hint = "목록·검색 도구가 돌려준 id 값을 그대로 넘기세요."
				}
				return nil, fmt.Sprintf("%s은(는) UUID 형식의 ID여야 합니다(받은 값: %q). %s", key, truncate(text, 40), hint)
			}
		}
		return text, ""
	case "integer":
		number, ok := numeric(value)
		if !ok {
			return nil, fmt.Sprintf("%s은(는) 정수여야 합니다(받은 값: %v)", key, value)
		}
		if number != math.Trunc(number) {
			return nil, fmt.Sprintf("%s은(는) 정수여야 합니다(받은 값: %v)", key, value)
		}
		return number, ""
	case "number":
		number, ok := numeric(value)
		if !ok {
			return nil, fmt.Sprintf("%s은(는) 숫자여야 합니다(받은 값: %v)", key, value)
		}
		return number, ""
	case "boolean":
		switch v := value.(type) {
		case bool:
			return v, ""
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true":
				return true, ""
			case "false":
				return false, ""
			}
		}
		return nil, fmt.Sprintf("%s은(는) true 또는 false여야 합니다(받은 값: %v)", key, value)
	case "array":
		if _, ok := value.([]any); !ok {
			return nil, fmt.Sprintf("%s은(는) 배열이어야 합니다", key)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Sprintf("%s은(는) 객체여야 합니다", key)
		}
	}
	return value, ""
}

// numeric accepts a JSON number or a numeric string. Handlers read numbers as
// float64, which is what encoding/json produces, so both become that.
func numeric(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func requiredNames(schema map[string]any) []string {
	switch required := schema["required"].(type) {
	case []string:
		return required
	case []any:
		out := make([]string, 0, len(required))
		for _, name := range required {
			if s, ok := name.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func truncate(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}
