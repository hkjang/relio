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
	if len(p.Categories) != 8 {
		t.Fatalf("R-05 asks for 8 types, preset has %d", len(p.Categories))
	}
	if !p.Workspace.Isolated || !p.Workspace.KnowledgeGate || p.Workspace.CustomerCodePattern != `^[0-9]{12}$` {
		t.Fatalf("workspace policy: %+v", p.Workspace)
	}
	keys := map[string]presetField{}
	for _, f := range p.Fields {
		keys[f.Key] = f
	}
	if !keys["service_type"].Required || keys["service_type"].Phase != "INTAKE" {
		t.Fatal("서비스 구분 must be a required intake field (R-01)")
	}
	if !contains(keys["dev_method"].Options, "미확인") {
		t.Fatal("개발방식 must offer 미확인 (R-01)")
	}
	if keys["resolver"].Phase != "RESOLUTION" {
		t.Fatal("해결 주체 is recorded at resolution (R-02)")
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
