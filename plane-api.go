package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/render"
)

var errPlaneFeatureIDRequired = errors.New("featureId is required (query feature_id or JSON body)")

// planeAPI mounts under the workspaceAPI group at /v1/projects/{projectID}/plane.
// The outer workspaceAPI scope already applies RequireAccount + RequireMember;
// these sub-groups add the role/subscription guards every other mutating route
// uses, so a VIEWER cannot store credentials, link, or trigger syncs.
func planeAPI(r chi.Router) {
	r.Route("/projects/{projectID}/plane", func(r chi.Router) {
		// Connection management stores encrypted Plane credentials -> admin only
		// (matches the invite routes' RequireSubscription + RequireAdmin).
		r.Group(func(r chi.Router) {
			r.Use(RequireSubscription())
			r.Use(RequireAdmin())
			r.Get("/connection", getPlaneConnection)
			r.Post("/connection", setPlaneConnection)
			r.Post("/connection/test", testPlaneConnection)
		})
		// Link/unlink/sync mutate board<->Plane state -> editor (matches the
		// feature + feature-comment routes' RequireSubscription + RequireEditor).
		r.Group(func(r chi.Router) {
			r.Use(RequireSubscription())
			r.Use(RequireEditor())
			r.Post("/link", linkFeatureToPlane)
			r.Delete("/link", unlinkFeatureFromPlane)
			r.Post("/sync", syncPlaneProject)
		})
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

type unlinkFeatureRequest struct {
	FeatureID string `json:"featureId"`
}

func (p *unlinkFeatureRequest) Bind(r *http.Request) error { return nil }

func unlinkFeatureFromPlane(w http.ResponseWriter, r *http.Request) {
	// Accept featureId from the query string or the JSON body so both curl-style
	// (`?feature_id=`) and structured callers (the CLI) work.
	fid := r.URL.Query().Get("feature_id")
	if fid == "" {
		data := &unlinkFeatureRequest{}
		if err := render.Bind(r, data); err == nil {
			fid = data.FeatureID
		}
	}
	if fid == "" {
		_ = render.Render(w, r, ErrInvalidRequest(errPlaneFeatureIDRequired))
		return
	}
	if err := GetEnv(r).Service.UnlinkFeatureFromPlane(fid); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, map[string]bool{"ok": true})
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
		pushed, pulled, serr := svc.SyncLink(r.Context(), link)
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
	res, err := GetEnv(r).Service.SyncProject(r.Context(), pid)
	if err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	render.JSON(w, r, res)
}
