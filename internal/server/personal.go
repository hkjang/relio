package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/personal"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// The personal workspace needs no extra permission: it stores pointers and
// querystrings for the signed-in user, and every read of the underlying record
// goes back through the scoped list queries.

func (s *Server) listSavedViews(w http.ResponseWriter, r *http.Request) {
	items, err := s.Personal.Views(r.Context(), principal(r), r.URL.Query().Get("resource"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createSavedView(w http.ResponseWriter, r *http.Request) {
	var in personal.SavedView
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Personal.SaveView(r.Context(), principal(r), in)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}

func (s *Server) updateSavedView(w http.ResponseWriter, r *http.Request) {
	var in personal.SavedView
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := s.Personal.UpdateView(r.Context(), principal(r), r.PathValue("id"), in); err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deleteSavedView(w http.ResponseWriter, r *http.Request) {
	if err := s.Personal.DeleteView(r.Context(), principal(r), r.PathValue("id")); err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFavorites(w http.ResponseWriter, r *http.Request) {
	if resource := r.URL.Query().Get("resource"); resource != "" {
		ids, err := s.Personal.FavoriteIDs(r.Context(), principal(r), resource)
		if err != nil {
			s.serviceError(w, r, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"ids": ids})
		return
	}
	items, err := s.Personal.Favorites(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) toggleFavorite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resource   string `json:"resource"`
		ResourceID string `json:"resourceId"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	favorited, err := s.Personal.ToggleFavorite(r.Context(), principal(r), in.Resource, in.ResourceID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"favorited": favorited})
}
