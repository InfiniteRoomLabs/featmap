package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
)

// planeAPI mounts under the workspaceAPI group at /v1/projects/{projectID}/plane.
func planeAPI(r chi.Router) {
	r.Route("/projects/{projectID}/plane", func(r chi.Router) {
		r.Get("/connection", getPlaneConnection)
		r.Post("/connection", setPlaneConnection)
		r.Post("/connection/test", testPlaneConnection)
		r.Post("/link", linkFeatureToPlane)
		r.Post("/sync", syncPlaneProject)
	})
}

func getPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	conn, err := GetEnv(r).Service.GetPlaneConnection(pid)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, conn) // APIKeyCipher/Nonce are json:"-"
}

type setPlaneConnectionRequest struct {
	BaseURL         string `json:"baseUrl"`
	PlaneWorkspace  string `json:"planeWorkspace"`
	APIKey          string `json:"apiKey"`
	WatchedProjects string `json:"watchedProjects"`
}

func (p *setPlaneConnectionRequest) Bind(r *http.Request) error { return nil }

func setPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	data := &setPlaneConnectionRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	conn, err := GetEnv(r).Service.SetPlaneConnection(pid, data.BaseURL, data.PlaneWorkspace, data.APIKey, data.WatchedProjects)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, conn)
}

func testPlaneConnection(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	if err := GetEnv(r).Service.TestPlaneConnection(pid); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, map[string]bool{"ok": true})
}

type linkFeatureRequest struct {
	FeatureID       string `json:"featureId"`
	PlaneProjectID  string `json:"planeProjectId"`
	PlaneWorkItemID string `json:"planeWorkItemId"`
}

func (p *linkFeatureRequest) Bind(r *http.Request) error { return nil }

func linkFeatureToPlane(w http.ResponseWriter, r *http.Request) {
	data := &linkFeatureRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	link, err := GetEnv(r).Service.LinkFeatureToPlane(data.FeatureID, data.PlaneProjectID, data.PlaneWorkItemID)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, link)
}

func syncPlaneProject(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "projectID")
	if fid := r.URL.Query().Get("feature_id"); fid != "" {
		svc := GetEnv(r).Service
		link, err := svc.GetPlaneLinkByFeature(fid)
		if err != nil {
			_ = render.Render(w, r, ErrInvalidRequest(err))
			return
		}
		pushed, pulled, serr := svc.SyncLink(link)
		now := time.Now().UTC()
		link.LastSyncedAt = &now
		if serr != nil {
			link.LastStatus = string(StatusError)
			link.LastError = serr.Error()
			svc.GetRepoObject().StorePlaneLink(link)
			_ = render.Render(w, r, ErrInvalidRequest(serr))
			return
		}
		link.LastStatus = string(StatusOK)
		link.LastError = ""
		svc.GetRepoObject().StorePlaneLink(link) // persist status + advanced cursor
		render.JSON(w, r, SyncResult{Pushed: pushed, Pulled: pulled, PerLink: []LinkSyncResult{{LinkID: link.ID, FeatureID: fid, Status: string(StatusOK), Pushed: pushed, Pulled: pulled}}})
		return
	}
	res, err := GetEnv(r).Service.SyncProject(pid)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, res)
}
