package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) updateContact(w http.ResponseWriter, r *http.Request) {
	var in crm.ContactInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.UpdateContact(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.DeleteContact(r.Context(), principal(r), r.PathValue("id"), s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.DeleteCustomer(r.Context(), principal(r), r.PathValue("id"), s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
