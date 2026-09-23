package voice

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
)

// A request can only be filed against a registered customer, and the sales
// customer form asks for what sales needs. A department handling member
// requests needs only the member's name and the code it already uses, so its
// workspace registers members with exactly those two, into the department.

type QuickCustomerInput struct {
	Name         string `json:"name"`
	CustomerCode string `json:"customerCode"`
}

// codeRule returns the label and compiled pattern a workspace uses for the
// customer code; a nil pattern means any code is accepted.
func codeRule(w Workspace) (string, *regexp.Regexp, error) {
	label := strings.TrimSpace(w.CustomerCodeLabel)
	if label == "" {
		label = "고객 코드"
	}
	if strings.TrimSpace(w.CustomerCodePattern) == "" {
		return label, nil, nil
	}
	pattern, err := regexp.Compile(w.CustomerCodePattern)
	if err != nil {
		return label, nil, fmt.Errorf("업무 영역의 %s 형식 설정이 올바르지 않습니다", label)
	}
	return label, pattern, nil
}

func checkQuickCustomer(w Workspace, in QuickCustomerInput) (QuickCustomerInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.CustomerCode = strings.TrimSpace(in.CustomerCode)
	label, pattern, err := codeRule(w)
	if err != nil {
		return in, err
	}
	if in.Name == "" {
		return in, errors.New("고객사명을 입력해야 합니다")
	}
	codeRequired := strings.TrimSpace(w.CustomerCodeLabel) != "" || pattern != nil
	if codeRequired && in.CustomerCode == "" {
		return in, fmt.Errorf("%s를 입력해야 합니다", label)
	}
	if pattern != nil && in.CustomerCode != "" && !pattern.MatchString(in.CustomerCode) {
		return in, fmt.Errorf("%s 형식이 올바르지 않습니다(받은 값: %s)", label, in.CustomerCode)
	}
	return in, nil
}

// QuickRegister creates a member with a name and a code, inside the
// workspace's department, from the intake screen without leaving it.
func (s *Service) QuickRegister(ctx context.Context, p *auth.Principal, workspaceID string, in QuickCustomerInput, m crm.RequestMeta) (crm.Customer, error) {
	if err := auth.Require(p, "voice:write"); err != nil {
		return crm.Customer{}, err
	}
	w, err := s.workspace(ctx, p, workspaceID)
	if err != nil {
		return crm.Customer{}, err
	}
	in, err = checkQuickCustomer(w, in)
	if err != nil {
		return crm.Customer{}, err
	}
	return s.CRM.CreateCustomerIn(ctx, p, crm.CustomerInput{Name: in.Name, CustomerCode: in.CustomerCode, CustomerType: "CUSTOMER"}, w.OrganizationID, w.ID, m)
}

type ImportRow struct {
	Name         string `json:"name"`
	CustomerCode string `json:"customerCode"`
}

type ImportOutcome struct {
	Line         int    `json:"line"`
	Name         string `json:"name"`
	CustomerCode string `json:"customerCode"`
	// Result is CREATED, UPDATED, UNCHANGED or ERROR.
	Result     string `json:"result"`
	Message    string `json:"message,omitempty"`
	CustomerID string `json:"customerId,omitempty"`
}

type ImportResult struct {
	Created   int             `json:"created"`
	Updated   int             `json:"updated"`
	Unchanged int             `json:"unchanged"`
	Failed    int             `json:"failed"`
	Items     []ImportOutcome `json:"items"`
}

// MaxImportRows bounds one import. A department's member list is a few
// thousand rows; beyond that the file is more likely a mistake.
const MaxImportRows = 5000

// ImportCustomers registers a department's member list. A code already
// registered is left alone, or renamed when updateNames is set, so the same
// file can be re-run as the periodic refresh. Every row is reported; one bad
// row never stops the rest.
func (s *Service) ImportCustomers(ctx context.Context, p *auth.Principal, workspaceID string, rows []ImportRow, updateNames bool, m crm.RequestMeta) (ImportResult, error) {
	if err := auth.Require(p, "voice:write"); err != nil {
		return ImportResult{}, err
	}
	if err := auth.Require(p, "customer:write"); err != nil {
		return ImportResult{}, err
	}
	if len(rows) == 0 {
		return ImportResult{}, errors.New("등록할 행이 없습니다")
	}
	if len(rows) > MaxImportRows {
		return ImportResult{}, fmt.Errorf("한 번에 %d행까지 등록할 수 있습니다(받은 행: %d)", MaxImportRows, len(rows))
	}
	w, err := s.workspace(ctx, p, workspaceID)
	if err != nil {
		return ImportResult{}, err
	}
	out := ImportResult{Items: make([]ImportOutcome, 0, len(rows))}
	seen := map[string]int{}
	for i, row := range rows {
		line := i + 1
		item := ImportOutcome{Line: line, Name: strings.TrimSpace(row.Name), CustomerCode: strings.TrimSpace(row.CustomerCode)}
		fail := func(message string) {
			item.Result, item.Message = "ERROR", message
			out.Failed++
			out.Items = append(out.Items, item)
		}
		clean, err := checkQuickCustomer(w, QuickCustomerInput{Name: row.Name, CustomerCode: row.CustomerCode})
		if err != nil {
			fail(err.Error())
			continue
		}
		if clean.CustomerCode != "" {
			if first, dup := seen[clean.CustomerCode]; dup {
				fail(fmt.Sprintf("같은 코드가 %d행에도 있습니다", first))
				continue
			}
			seen[clean.CustomerCode] = line
			if existing, err := s.CRM.CustomerByCode(ctx, p, clean.CustomerCode); err == nil {
				item.CustomerID = existing.ID
				if updateNames && existing.Name != clean.Name {
					in := crm.CustomerInput{Name: clean.Name, RegistrationNo: existing.RegistrationNo, CustomerCode: existing.CustomerCode,
						CustomerType: existing.CustomerType, Grade: existing.Grade, Industry: existing.Industry, Website: existing.Website,
						Phone: existing.Phone, Email: existing.Email, Address: existing.Address, OwnerID: existing.OwnerID,
						Health: existing.Health, AnnualRevenue: existing.AnnualRevenue, EmployeeCount: existing.EmployeeCount,
						CustomFields: existing.CustomFields, Version: existing.Version}
					if _, err = s.CRM.UpdateCustomer(ctx, p, existing.ID, in, m); err != nil {
						fail(err.Error())
						continue
					}
					item.Result, item.Message = "UPDATED", "고객사명을 "+existing.Name+"에서 바꿨습니다"
					out.Updated++
				} else {
					item.Result = "UNCHANGED"
					out.Unchanged++
				}
				out.Items = append(out.Items, item)
				continue
			}
		}
		created, err := s.CRM.CreateCustomerIn(ctx, p, crm.CustomerInput{Name: clean.Name, CustomerCode: clean.CustomerCode, CustomerType: "CUSTOMER"}, w.OrganizationID, w.ID, m)
		if err != nil {
			if errors.Is(err, crm.ErrCustomerCodeTaken) {
				// Taken by a customer this user cannot see: another department's.
				fail("이미 다른 부서 고객에 등록된 코드입니다. 관리자에게 확인하세요")
			} else {
				fail(err.Error())
			}
			continue
		}
		item.Result, item.CustomerID = "CREATED", created.ID
		out.Created++
		out.Items = append(out.Items, item)
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: "CUSTOMER_IMPORT",
		Resource: "voice_workspace", ResourceID: w.ID,
		After: map[string]any{"rows": len(rows), "created": out.Created, "updated": out.Updated, "unchanged": out.Unchanged, "failed": out.Failed},
		IP:    m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	return out, nil
}
