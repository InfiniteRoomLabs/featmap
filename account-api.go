package main

import (
	"log"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
)

func accountAPI(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(RequireAccount())

		r.Get("/app", getApp)

		r.Route("/emailupdate/{EMAIL}", func(r chi.Router) {
			r.Post("/", updateEmail)
		})

		r.Post("/nameupdate", updateName)
		r.Post("/resend", resend)
		r.Post("/delete", deleteAccount)

		r.Post("/workspaces", createWorkspace)

		r.Get("/apikeys", listAPIKeys)
		r.Post("/apikeys", createAPIKey)
		r.Delete("/apikeys/{ID}", revokeAPIKey)

	})
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func (p *createAPIKeyRequest) Bind(r *http.Request) error { return nil }

type createAPIKeyResponse struct {
	APIKey    *APIKey `json:"apiKey"`
	Plaintext string  `json:"plaintext"`
}

func listAPIKeys(w http.ResponseWriter, r *http.Request) {
	s := GetEnv(r).Service
	keys, err := s.ListAPIKeys()
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, keys)
}

func createAPIKey(w http.ResponseWriter, r *http.Request) {
	data := &createAPIKeyRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	s := GetEnv(r).Service
	plaintext, key, err := s.CreateAPIKey(data.Name)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, createAPIKeyResponse{APIKey: key, Plaintext: plaintext})
}

func revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ID")
	s := GetEnv(r).Service
	if err := s.RevokeAPIKey(id); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
}

func getApp(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Mode          string          `json:"mode"`
		Account       *Account        `json:"account"`
		Workspaces    []*Workspace    `json:"workspaces"`
		Memberships   []*Member       `json:"memberships"`
		Subscriptions []*Subscription `json:"subscriptions"`
	}

	s := GetEnv(r).Service

	s.UpdateLatestActivityNow()

	ss := s.GetSubscriptionsByAccount()
	log.Println(len(ss))
	render.JSON(w, r, response{
		Mode:          s.GetConfig().Mode,
		Account:       s.GetAccountObject(),
		Workspaces:    s.GetWorkspaces(),
		Memberships:   s.GetMembersByAccount(),
		Subscriptions: s.GetSubscriptionsByAccount(),
	})
}

func updateEmail(w http.ResponseWriter, r *http.Request) {
	email := chi.URLParam(r, "EMAIL")

	s := GetEnv(r).Service
	err := s.UpdateEmail(email)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	return
}

type updateNameRequest struct {
	Name string `json:"name"`
}

func (p *updateNameRequest) Bind(r *http.Request) error {
	return nil
}

func updateName(w http.ResponseWriter, r *http.Request) {
	data := &updateNameRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	s := GetEnv(r).Service
	err := s.UpdateName(data.Name)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	return
}

func deleteAccount(w http.ResponseWriter, r *http.Request) {

	s := GetEnv(r).Service
	err := s.DeleteAccount()
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	return
}

func resend(w http.ResponseWriter, r *http.Request) {

	s := GetEnv(r).Service
	err := s.ResendEmail()
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	return
}

// Workspace
type createWorkspaceRequest struct {
	Name string `json:"name"`
}

func (p *createWorkspaceRequest) Bind(r *http.Request) error {
	return nil
}
func createWorkspace(w http.ResponseWriter, r *http.Request) {
	data := &createWorkspaceRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	s := GetEnv(r).Service
	workspace, _, _, err := s.CreateWorkspace(data.Name)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, workspace)
}
