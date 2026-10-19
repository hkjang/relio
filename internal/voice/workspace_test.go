package voice

import (
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/auth"
)

var intakeFields = []Field{
	{Key: "service_type", Label: "서비스 구분", Type: "Select", Required: true, Phase: "INTAKE", Options: []string{"휴대폰본인확인", "아이핀"}},
	{Key: "dev_method", Label: "개발방식", Type: "Select", Phase: "INTAKE", Options: []string{"팝업(API)", "미확인"}},
	{Key: "error_code", Label: "오류코드", Type: "Text", Phase: "INTAKE"},
	{Key: "resolver", Label: "해결 주체", Type: "Select", Phase: "RESOLUTION", Options: []string{"회원사 조치", "KCB IT"}},
}

func TestIntakeRequiresTheRequiredFieldsOfItsPhaseOnly(t *testing.T) {
	_, err := validateFieldValues(intakeFields, "INTAKE", map[string]any{"error_code": "E1003"}, true)
	if err == nil || !strings.Contains(err.Error(), "서비스 구분") {
		t.Fatalf("missing 서비스 구분 must be named, got %v", err)
	}
	// 해결 주체 is a resolution field: not asked for at intake.
	got, err := validateFieldValues(intakeFields, "INTAKE", map[string]any{"service_type": "아이핀"}, true)
	if err != nil || got["service_type"] != "아이핀" {
		t.Fatalf("intake with the required field: %v %v", got, err)
	}
}

func TestSelectValuesMustBeOneOfTheOptions(t *testing.T) {
	_, err := validateFieldValues(intakeFields, "INTAKE", map[string]any{"service_type": "없는서비스"}, true)
	if err == nil || !strings.Contains(err.Error(), "휴대폰본인확인") {
		t.Fatalf("the options must be listed so the caller can fix it, got %v", err)
	}
}

func TestUnknownFieldsAreRefusedAndBlanksMeanAbsent(t *testing.T) {
	if _, err := validateFieldValues(intakeFields, "INTAKE", map[string]any{"service_type": "아이핀", "servce_type": "x"}, true); err == nil {
		t.Fatal("a misspelled key must not be stored silently")
	}
	got, err := validateFieldValues(intakeFields, "INTAKE", map[string]any{"service_type": "아이핀", "error_code": "   ", "dev_method": nil}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := got["error_code"]; kept {
		t.Fatalf("a blank optional value is absent, not an empty string: %v", got)
	}
}

func TestConfiguredDefaultIsValidatedAndStored(t *testing.T) {
	defs := []Field{{Key: "dev_method", Label: "개발방식", Type: "Select", Phase: "INTAKE", Required: true,
		Options: []string{"DIRECT", "UNKNOWN"}, OptionLabels: map[string]string{"DIRECT": "직접개발", "UNKNOWN": "미확인"}, DefaultValue: "UNKNOWN"}}
	got, err := validateFieldValues(defs, "INTAKE", map[string]any{}, true)
	if err != nil || got["dev_method"] != "UNKNOWN" {
		t.Fatalf("configured default was not materialized: got=%v err=%v", got, err)
	}
	defs[0].DefaultValue = "INVENTED"
	if _, err = validateFieldValues(defs, "INTAKE", map[string]any{}, true); err == nil || !strings.Contains(err.Error(), "기본값 설정") {
		t.Fatalf("an invalid configured default must fail loudly: %v", err)
	}
}

func TestFieldPatchClearsOptionalValuesWithoutRestoringDefaults(t *testing.T) {
	defs := []Field{
		{Key: "optional", Label: "선택값", Type: "Text", Phase: "INTAKE", DefaultValue: "DEFAULT"},
		{Key: "required", Label: "필수값", Type: "Text", Phase: "INTAKE", Required: true, DefaultValue: "DEFAULT"},
		{Key: "resolver", Label: "해결 주체", Type: "Select", Phase: "RESOLUTION", Required: true, Options: []string{"OPS"}},
	}
	for _, blank := range []any{nil, "", "  "} {
		patch, err := validateFieldValues(defs, "", map[string]any{"optional": blank}, false)
		if err != nil || len(patch) != 1 || patch["optional"] != nil {
			t.Fatalf("explicit clear was lost: %#v %v", patch, err)
		}
		defaults, err := requiredFieldDefaults(defs, "INTAKE", map[string]any{"required": "stored", "optional": patch["optional"]})
		if err != nil || len(defaults) != 0 {
			t.Fatalf("clearing optional value restored default or required resolution: %#v %v", defaults, err)
		}
		if _, err := requiredFieldDefaults(defs, "INTAKE", map[string]any{"required": blank}); err == nil || !strings.Contains(err.Error(), "필수값") {
			t.Fatalf("required value may not be cleared even with a default: %v", err)
		}
	}
	// Omission can use a configured default; explicit empty values cannot.
	got, err := validateFieldValues(defs, "INTAKE", map[string]any{"optional": nil}, true)
	if err != nil || got["required"] != "DEFAULT" || got["optional"] != nil {
		t.Fatalf("default/explicit-empty distinction: %#v %v", got, err)
	}
	if _, err := validateFieldValues(defs, "INTAKE", map[string]any{"required": nil}, true); err == nil {
		t.Fatal("an explicitly cleared required field must fail, not silently default")
	}
	for _, empty := range []any{[]any{}, []string{}} {
		if _, err := requiredFieldDefaults(defs, "INTAKE", map[string]any{"required": empty}); err == nil {
			t.Fatal("an empty collection cannot satisfy a required field")
		}
	}
}

func TestSelectErrorNamesStableCodeAndDisplayName(t *testing.T) {
	defs := []Field{{Key: "resolver", Label: "해결 주체", Type: "Select", Phase: "RESOLUTION", Options: []string{"OPS"}, OptionLabels: map[string]string{"OPS": "운영 조치"}}}
	_, err := validateFieldValues(defs, "RESOLUTION", map[string]any{"resolver": "OTHER"}, false)
	if err == nil || !strings.Contains(err.Error(), "OPS(운영 조치)") {
		t.Fatalf("allowed code and label must be returned together: %v", err)
	}
}

func TestResolutionFieldsAreCheckedWithoutReopeningIntake(t *testing.T) {
	resolution := phaseFields(intakeFields, "RESOLUTION")
	if len(resolution) != 1 || resolution[0].Key != "resolver" {
		t.Fatalf("phaseFields = %v", resolution)
	}
	// An intake value whose option was later retired must not block closing
	// the case: only resolution fields are checked at resolution.
	stored := map[string]any{"service_type": "폐기된 옵션", "resolver": "KCB IT"}
	if _, err := validateFieldValues(resolution, "RESOLUTION", onlyDefined(resolution, stored), true); err != nil {
		t.Fatalf("resolution check tripped over an intake value: %v", err)
	}
}

func TestMemberCodeRule(t *testing.T) {
	w := Workspace{CustomerCodeLabel: "회원사코드", CustomerCodePattern: `^[0-9]{12}$`}
	cases := map[string]bool{"123456789012": true, "12345678901": false, "1234567890123": false, "12345678901a": false, "": false}
	for code, ok := range cases {
		_, err := checkQuickCustomer(w, QuickCustomerInput{Name: "회원사", CustomerCode: code})
		if (err == nil) != ok {
			t.Fatalf("code %q: err=%v, want ok=%v", code, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "회원사코드") {
			t.Fatalf("the workspace's own label must appear: %v", err)
		}
	}
	if _, err := checkQuickCustomer(w, QuickCustomerInput{Name: "  ", CustomerCode: "123456789012"}); err == nil {
		t.Fatal("a name is required")
	}
	// A workspace without a code rule accepts a name alone.
	if _, err := checkQuickCustomer(Workspace{}, QuickCustomerInput{Name: "고객"}); err != nil {
		t.Fatalf("no code rule: %v", err)
	}
}

func TestWorkspaceValidation(t *testing.T) {
	good := WorkspaceInput{Code: "MEMBER_SUPPORT", Name: "회원사 민원", Isolated: true, OrganizationID: "org"}
	if err := ValidateWorkspace(good); err != nil {
		t.Fatal(err)
	}
	isolatedWithoutOwner := good
	isolatedWithoutOwner.OrganizationID = ""
	if err := ValidateWorkspace(isolatedWithoutOwner); err == nil {
		t.Fatal("an isolated workspace needs an owning organisation, or nobody could see it")
	}
	badPattern := good
	badPattern.CustomerCodePattern = "^[0-9"
	if err := ValidateWorkspace(badPattern); err == nil {
		t.Fatal("an invalid code pattern must be refused at save time, not at registration")
	}
	for _, code := range []string{"M", "9LIVES", "HAS SPACE", "MEMBER-SUPPORT"} {
		if err := ValidateWorkspace(WorkspaceInput{Code: code, Name: "x"}); err == nil {
			t.Fatalf("code %q must be refused", code)
		}
	}
	// Lower case is accepted and stored upper case, like request type codes.
	if err := ValidateWorkspace(WorkspaceInput{Code: "member_support", Name: "x"}); err != nil {
		t.Fatalf("lower-case code should be normalised, got %v", err)
	}
}

func TestPresetMatchesTheRequest(t *testing.T) {
	p := memberSupportPreset
	if !p.Workspace.Isolated || !p.Workspace.KnowledgeGate {
		t.Fatalf("workspace policy: %+v", p.Workspace)
	}
	// Operational codes, required flags, defaults and patterns belong to the
	// department. The product preset must not invent them.
	if p.Workspace.CustomerCodePattern != "" {
		t.Fatalf("an unconfirmed member-code pattern was baked into the preset: %q", p.Workspace.CustomerCodePattern)
	}
	for _, r := range p.Roles {
		review := contains(r.Permissions, "voice:knowledge-review")
		if review != (r.Code == "MEMBER_SUPPORT_REVIEWER") {
			t.Fatalf("only the reviewer role may judge knowledge (R-04): %s", r.Code)
		}
		if r.DataScope != "DEPARTMENT" {
			t.Fatalf("%s must be department scoped (R-08)", r.Code)
		}
	}
}

func validPresetApplication() PresetApplication {
	categories := make([]PresetCategoryInput, 8)
	for i := range categories {
		categories[i] = PresetCategoryInput{Code: "TYPE_" + string(rune('A'+i)), Name: "유형", VoiceType: "COMPLAINT"}
	}
	return PresetApplication{
		OrganizationID: "11111111-1111-1111-1111-111111111111", CustomerCodeUniqueScope: "GLOBAL", Categories: categories,
		ServiceType:       PresetFieldInput{Values: []CodedValue{{Code: "SERVICE_A", Name: "서비스 A"}}},
		DevelopmentMethod: PresetFieldInput{Values: []CodedValue{{Code: "DEV_A", Name: "개발 A"}}},
		Resolver: PresetFieldInput{Values: []CodedValue{{Code: "OWNER_A", Name: "주체 A"}, {Code: "OWNER_B", Name: "주체 B"},
			{Code: "OWNER_C", Name: "주체 C"}, {Code: "OWNER_D", Name: "주체 D"}}},
	}
}

func TestPresetRequiresConfirmedDepartmentValues(t *testing.T) {
	in := validPresetApplication()
	if err := validatePresetApplication(in); err != nil {
		t.Fatal(err)
	}
	in.Categories = in.Categories[:7]
	if err := validatePresetApplication(in); err == nil || !strings.Contains(err.Error(), "8개") {
		t.Fatalf("seven categories must be refused: %v", err)
	}
	in = validPresetApplication()
	in.Resolver.Values = in.Resolver.Values[:3]
	if err := validatePresetApplication(in); err == nil || !strings.Contains(err.Error(), "4개") {
		t.Fatalf("three resolvers must be refused: %v", err)
	}
	in = validPresetApplication()
	in.ServiceType.DefaultValue = "UNKNOWN"
	if err := validatePresetApplication(in); err == nil || !strings.Contains(err.Error(), "기본값") {
		t.Fatalf("an unknown default must be refused: %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestIsolationPredicateIsSkippedOnlyForAdministrators(t *testing.T) {
	admin := &auth.Principal{IsBootstrap: true}
	if strings.Contains(VisibleSQL(admin, "v"), "voice_workspaces") {
		t.Fatal("a system administrator is not isolated")
	}
	user := &auth.Principal{DataScope: "COMPANY"}
	if !strings.Contains(VisibleSQL(user, "v"), "iso.isolated") {
		t.Fatal("a COMPANY-scope user must still be kept out of isolated workspaces")
	}
	if !strings.Contains(SalesSignalSQL("v"), "iso.isolated") {
		t.Fatal("sales metrics must exclude isolated requests")
	}
}
