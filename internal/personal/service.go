// Package personal holds the per-user workspace: the searches someone keeps and
// the records they starred. Nothing here grants access to data. A saved view is
// replayed through the normal scoped list endpoint, and a favorite is resolved by
// re-reading the record under the caller's Data Scope, so a starred customer that
// moves to another team simply stops appearing.
package personal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ DB *pgxpool.Pool }

type SavedView struct {
	ID        string    `json:"id"`
	Resource  string    `json:"resource"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	Pinned    bool      `json:"pinned"`
	Order     int       `json:"displayOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

type Favorite struct {
	Resource   string    `json:"resource"`
	ResourceID string    `json:"resourceId"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle,omitempty"`
	Route      string    `json:"route"`
	CreatedAt  time.Time `json:"createdAt"`
}

var viewResources = map[string]bool{"CUSTOMER": true, "OPPORTUNITY": true, "VOICE": true, "ACTIVITY": true, "CONTRACT": true}
var favoriteResources = map[string]bool{"CUSTOMER": true, "OPPORTUNITY": true, "VOICE": true, "CONTRACT": true}

// maxQueryLength keeps a saved view to something a URL can carry. The query is
// replayed verbatim, so it is stored as opaque text and never parsed here.
const maxQueryLength = 600

func normalizeResource(value string, allowed map[string]bool) (string, error) {
	resource := strings.ToUpper(strings.TrimSpace(value))
	if !allowed[resource] {
		return "", fmt.Errorf("invalid resource %q", value)
	}
	return resource, nil
}

func (s *Service) Views(ctx context.Context, p *auth.Principal, resource string) ([]SavedView, error) {
	query := `SELECT id,resource,name,query,pinned,display_order,created_at FROM user_saved_views
		WHERE user_id=$1 AND ($2='' OR resource=$2) ORDER BY resource,display_order,name`
	rows, err := s.DB.Query(ctx, query, p.UserID, strings.ToUpper(strings.TrimSpace(resource)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedView{}
	for rows.Next() {
		var v SavedView
		if err = rows.Scan(&v.ID, &v.Resource, &v.Name, &v.Query, &v.Pinned, &v.Order, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) SaveView(ctx context.Context, p *auth.Principal, in SavedView) (SavedView, error) {
	resource, err := normalizeResource(in.Resource, viewResources)
	if err != nil {
		return SavedView{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 40 {
		return SavedView{}, errors.New("name is required and must be 40 characters or fewer")
	}
	query := strings.TrimPrefix(strings.TrimSpace(in.Query), "?")
	if len(query) > maxQueryLength {
		return SavedView{}, fmt.Errorf("query must be %d characters or fewer", maxQueryLength)
	}
	// Saving the same name again updates it, which is what a user expects when
	// they refine a filter and save it under the name they already chose.
	id := in.ID
	if id == "" {
		id = ids.New()
	}
	var out SavedView
	err = s.DB.QueryRow(ctx, `INSERT INTO user_saved_views(id,user_id,resource,name,query,pinned,display_order)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(user_id,resource,name) DO UPDATE SET query=excluded.query,pinned=excluded.pinned,
			display_order=excluded.display_order,updated_at=now()
		RETURNING id,resource,name,query,pinned,display_order,created_at`,
		id, p.UserID, resource, name, query, in.Pinned, in.Order).
		Scan(&out.ID, &out.Resource, &out.Name, &out.Query, &out.Pinned, &out.Order, &out.CreatedAt)
	return out, err
}

func (s *Service) UpdateView(ctx context.Context, p *auth.Principal, id string, in SavedView) error {
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 40 {
		return errors.New("name is required and must be 40 characters or fewer")
	}
	command, err := s.DB.Exec(ctx, `UPDATE user_saved_views SET name=$3,pinned=$4,display_order=$5,updated_at=now()
		WHERE id=$1 AND user_id=$2`, id, p.UserID, name, in.Pinned, in.Order)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("saved view not found")
	}
	return nil
}

func (s *Service) DeleteView(ctx context.Context, p *auth.Principal, id string) error {
	command, err := s.DB.Exec(ctx, `DELETE FROM user_saved_views WHERE id=$1 AND user_id=$2`, id, p.UserID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("saved view not found")
	}
	return nil
}

// ToggleFavorite stars or unstars a record and reports the resulting state.
func (s *Service) ToggleFavorite(ctx context.Context, p *auth.Principal, resource, resourceID string) (bool, error) {
	kind, err := normalizeResource(resource, favoriteResources)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(resourceID) == "" {
		return false, errors.New("resourceId is required")
	}
	// Only a record the caller can already read may be starred, otherwise the
	// favorites list would confirm the existence of out-of-scope records.
	visible, err := s.visible(ctx, p, kind, resourceID)
	if err != nil {
		return false, err
	}
	if !visible {
		return false, errors.New("record not found")
	}
	command, err := s.DB.Exec(ctx, `DELETE FROM user_favorites WHERE user_id=$1 AND resource=$2 AND resource_id=$3`,
		p.UserID, kind, resourceID)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() > 0 {
		return false, nil
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO user_favorites(user_id,resource,resource_id) VALUES($1,$2,$3)
		ON CONFLICT DO NOTHING`, p.UserID, kind, resourceID)
	return err == nil, err
}

// favoriteSources maps each resource to the scoped lookup used for both the
// visibility check and the display text.
var favoriteSources = map[string]struct {
	table    string
	title    string
	subtitle string
	route    string
}{
	"CUSTOMER":    {"customers", "x.name", "COALESCE(x.industry,'')", "/app/customers/"},
	"OPPORTUNITY": {"opportunities", "x.name", "(SELECT c.name FROM customers c WHERE c.id=x.customer_id)", "/app/opportunities"},
	"VOICE":       {"customer_voices", "x.title", "x.voice_no", "/app/voices"},
	"CONTRACT":    {"contracts", "x.title", "x.contract_no", "/app/contracts"},
}

func (s *Service) visible(ctx context.Context, p *auth.Principal, resource, id string) (bool, error) {
	source := favoriteSources[resource]
	var exists bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+source.table+` x WHERE x.id=$4 AND `+crm.ScopeSQL("x")+`)`,
		p.DataScope, p.UserID, orgArg(p), id).Scan(&exists)
	return exists, err
}

func orgArg(p *auth.Principal) any {
	if p.OrganizationID == "" {
		return nil
	}
	return p.OrganizationID
}

func (s *Service) Favorites(ctx context.Context, p *auth.Principal) ([]Favorite, error) {
	out := []Favorite{}
	// One scoped query per resource keeps the Data Scope predicate identical to
	// the list screens rather than reimplementing it across a union.
	for _, resource := range []string{"CUSTOMER", "OPPORTUNITY", "VOICE", "CONTRACT"} {
		source := favoriteSources[resource]
		rows, err := s.DB.Query(ctx, `SELECT x.id::text,`+source.title+`,`+source.subtitle+`,f.created_at
			FROM user_favorites f JOIN `+source.table+` x ON x.id=f.resource_id
			WHERE f.user_id=$4 AND f.resource=$5 AND `+crm.ScopeSQL("x")+`
			ORDER BY f.created_at DESC`,
			p.DataScope, p.UserID, orgArg(p), p.UserID, resource)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f Favorite
			if err = rows.Scan(&f.ResourceID, &f.Title, &f.Subtitle, &f.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			f.Resource = resource
			f.Route = source.route
			if resource == "CUSTOMER" {
				f.Route += f.ResourceID
			}
			out = append(out, f)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FavoriteIDs lets a list screen render the star state without one call per row.
func (s *Service) FavoriteIDs(ctx context.Context, p *auth.Principal, resource string) ([]string, error) {
	kind, err := normalizeResource(resource, favoriteResources)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT resource_id::text FROM user_favorites WHERE user_id=$1 AND resource=$2`, p.UserID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
