package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func Test_SetGetPlaneConnection_encrypts(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		// service config must carry an encryption key for this test
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)

		conn, err := s.SetPlaneConnection(fx.Project.ID, "https://api.plane.so", "ws-slug", "secret_key_1234", "p1,p2")
		mustOK(t, err, "SetPlaneConnection")
		if conn.APIKeyHint != "1234" {
			t.Fatalf("hint should be last 4, got %q", conn.APIKeyHint)
		}
		if string(conn.APIKeyCipher) == "secret_key_1234" {
			t.Fatal("api key stored in plaintext")
		}

		got, err := s.GetPlaneConnection(fx.Project.ID)
		mustOK(t, err, "GetPlaneConnection")
		if got.PlaneWorkspace != "ws-slug" || got.WatchedProjects != "p1,p2" {
			t.Fatalf("connection round-trip mismatch: %+v", got)
		}
	})
}

func Test_LinkUnlinkFeature(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)
		feat := fx.Features[0]

		link, err := s.LinkFeatureToPlane(feat.ID, "plane-proj", "WI-1")
		mustOK(t, err, "LinkFeatureToPlane")
		if link.PlaneWorkItemID != "WI-1" {
			t.Fatalf("bad link: %+v", link)
		}
		got, err := s.GetPlaneLinkByFeature(feat.ID)
		mustOK(t, err, "GetPlaneLinkByFeature")
		if got.ID != link.ID {
			t.Fatal("link id mismatch")
		}
		if err := s.UnlinkFeatureFromPlane(feat.ID); err != nil {
			t.Fatalf("unlink: %v", err)
		}
		if _, err := s.GetPlaneLinkByFeature(feat.ID); err == nil {
			t.Fatal("expected link gone after unlink")
		}
	})
}

// fakePlane is an in-memory Plane double: comments created via POST are returned
// by subsequent GETs, so a sync run sees its own pushes (the echo risk).
type fakePlane struct {
	mu       sync.Mutex
	comments []PlaneComment
	seq      int
	srv      *httptest.Server
}

func newFakePlane() *fakePlane {
	f := &fakePlane{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			f.seq++
			id := "plane-" + strconv.Itoa(f.seq)
			cm := PlaneComment{ID: id, CommentHTML: "<p>pushed</p>", UpdatedAt: time.Now().UTC(), Actor: "remote"}
			f.comments = append(f.comments, cm)
			b, _ := json.Marshal(cm)
			_, _ = w.Write(b)
			return
		}
		// GET list
		b, _ := json.Marshal(planeCommentList{Results: f.comments, NextPageResults: false})
		_, _ = w.Write(b)
	}))
	return f
}

// seedRemote adds a comment that did NOT originate from featmap (a real Plane-side post).
func (f *fakePlane) seedRemote(html string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.comments = append(f.comments, PlaneComment{ID: "remote-" + strconv.Itoa(f.seq), CommentHTML: html, UpdatedAt: time.Now().UTC(), Actor: "remote"})
}

func Test_SyncLink_noEcho_and_pull(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)
		feat := fx.Features[1] // a feature with no seeded comment

		fp := newFakePlane()
		defer fp.srv.Close()

		// connection pointing at the fake; inject the fake's http client by
		// storing the fake URL as base_url (PlaneClient uses default client, which
		// reaches httptest's real URL).
		_, err := s.SetPlaneConnection(fx.Project.ID, fp.srv.URL, "ws", "key", "")
		mustOK(t, err, "SetPlaneConnection")
		link, err := s.LinkFeatureToPlane(feat.ID, "pp", "wi")
		mustOK(t, err, "link")

		// 1 local comment to push
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "local one")
		mustOK(t, err, "local comment")

		// first sync: push 1, pull sees its own push -> must NOT re-import (echo)
		pushed, pulled, err := s.SyncLink(ctx, link)
		mustOK(t, err, "sync 1")
		if pushed != 1 {
			t.Fatalf("expected 1 pushed, got %d", pushed)
		}
		if pulled != 0 {
			t.Fatalf("echo! pulled own pushed comment: %d", pulled)
		}

		// a genuine remote comment appears
		fp.seedRemote("<p>from plane</p>")
		pushed2, pulled2, err := s.SyncLink(ctx, link)
		mustOK(t, err, "sync 2")
		if pushed2 != 0 {
			t.Fatalf("nothing new to push, got %d", pushed2)
		}
		if pulled2 != 1 {
			t.Fatalf("expected to pull 1 remote, got %d", pulled2)
		}

		// idempotency: a third run changes nothing
		p3, q3, err := s.SyncLink(ctx, link)
		mustOK(t, err, "sync 3")
		if p3 != 0 || q3 != 0 {
			t.Fatalf("third run not idempotent: pushed=%d pulled=%d", p3, q3)
		}
	})
}

// newFakePlaneFailingWorkItem returns a fake Plane that serves comment lists
// normally for every work item EXCEPT failWorkItemID, for which it returns 500
// (the work item id appears in the request path:
// /api/v1/.../work-items/{id}/comments/). This lets a single SyncProject run
// exercise one failing link and one succeeding link.
func newFakePlaneFailingWorkItem(failWorkItemID string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/work-items/"+failWorkItemID+"/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// no comments on the healthy work item -> a clean ok run
		b, _ := json.Marshal(planeCommentList{Results: []PlaneComment{}, NextPageResults: false})
		_, _ = w.Write(b)
	}))
}

// Test_SyncProject_perLinkStatus covers the only path that persists per-link
// status/cursor. A project has two links; one work item's remote fetch fails.
// SyncProject must mark that link LastStatus=error, continue to the other link
// and mark it ok, and StorePlaneLink BOTH (verified by reading the persisted
// rows back from the DB, not just the in-memory SyncResult).
func Test_SyncProject_perLinkStatus(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)
		okFeat := fx.Features[1]   // healthy link
		failFeat := fx.Features[2] // link whose remote fetch 500s

		srv := newFakePlaneFailingWorkItem("wi-fail")
		defer srv.Close()

		_, err := s.SetPlaneConnection(fx.Project.ID, srv.URL, "ws", "key", "")
		mustOK(t, err, "SetPlaneConnection")

		okLink, err := s.LinkFeatureToPlane(okFeat.ID, "pp", "wi-ok")
		mustOK(t, err, "link ok")
		failLink, err := s.LinkFeatureToPlane(failFeat.ID, "pp", "wi-fail")
		mustOK(t, err, "link fail")

		res, err := s.SyncProject(ctx, fx.Project.ID)
		mustOK(t, err, "SyncProject") // SyncProject itself does not surface per-link errors
		if len(res.PerLink) != 2 {
			t.Fatalf("expected 2 per-link results, got %d", len(res.PerLink))
		}

		// per-link result statuses (in-memory SyncResult)
		statusByLink := map[string]string{}
		errByLink := map[string]string{}
		for _, lr := range res.PerLink {
			statusByLink[lr.LinkID] = lr.Status
			errByLink[lr.LinkID] = lr.Error
		}
		if statusByLink[okLink.ID] != string(StatusOK) {
			t.Fatalf("ok link result status = %q, want ok", statusByLink[okLink.ID])
		}
		if statusByLink[failLink.ID] != string(StatusError) {
			t.Fatalf("fail link result status = %q, want error", statusByLink[failLink.ID])
		}
		if errByLink[failLink.ID] == "" {
			t.Fatal("fail link result should carry a non-empty error")
		}

		// persistence: both links must have been StorePlaneLink'd. Read fresh
		// rows from the DB to prove the status/error/last_synced_at were saved.
		gotOK, err := s.GetPlaneLinkByFeature(okFeat.ID)
		mustOK(t, err, "reload ok link")
		if gotOK.LastStatus != string(StatusOK) {
			t.Fatalf("persisted ok link status = %q, want ok", gotOK.LastStatus)
		}
		if gotOK.LastError != "" {
			t.Fatalf("persisted ok link should have empty error, got %q", gotOK.LastError)
		}
		if gotOK.LastSyncedAt == nil {
			t.Fatal("persisted ok link LastSyncedAt not set")
		}

		gotFail, err := s.GetPlaneLinkByFeature(failFeat.ID)
		mustOK(t, err, "reload fail link")
		if gotFail.LastStatus != string(StatusError) {
			t.Fatalf("persisted fail link status = %q, want error", gotFail.LastStatus)
		}
		if gotFail.LastError == "" {
			t.Fatal("persisted fail link should carry a non-empty error")
		}
		if gotFail.LastSyncedAt == nil {
			t.Fatal("persisted fail link LastSyncedAt not set")
		}
	})
}

// Test_SyncLink_pushError_continues is the regression guard for the critical
// bug where a single failed push aborted the whole batch AND the pull phase.
// A fake Plane rejects (422) any comment whose body contains "REJECT" but
// accepts others. With local comments [ok1, REJECT-me, ok2] and a remote
// comment present, one bad push must NOT stop the other pushes or the pull.
func Test_SyncLink_pushError_continues(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)
		feat := fx.Features[3] // a feature with no seeded comment

		var seq int
		remote := []PlaneComment{{ID: "remote-1", CommentHTML: "<p>from plane</p>", UpdatedAt: time.Now().UTC(), Actor: "remote"}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "REJECT") {
					w.WriteHeader(422) // Plane rejects this one comment
					return
				}
				seq++
				cm := PlaneComment{ID: "pushed-" + strconv.Itoa(seq), CommentHTML: "<p>ok</p>", UpdatedAt: time.Now().UTC(), Actor: "me"}
				b, _ := json.Marshal(cm)
				_, _ = w.Write(b)
				return
			}
			b, _ := json.Marshal(planeCommentList{Results: remote, NextPageResults: false})
			_, _ = w.Write(b)
		}))
		defer srv.Close()

		_, err := s.SetPlaneConnection(fx.Project.ID, srv.URL, "ws", "key", "")
		mustOK(t, err, "SetPlaneConnection")
		link, err := s.LinkFeatureToPlane(feat.ID, "pp", "wi")
		mustOK(t, err, "link")

		// order matters: bad comment in the MIDDLE, so we prove later ones still push
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "ok one")
		mustOK(t, err, "c1")
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "please REJECT this")
		mustOK(t, err, "c2")
		_, err = s.CreateFeatureCommentWithID(newUUID(), feat.ID, "ok two")
		mustOK(t, err, "c3")

		pushed, pulled, serr := s.SyncLink(ctx, link)
		if serr == nil {
			t.Fatal("expected the rejected comment to surface an error")
		}
		if pushed != 2 {
			t.Fatalf("expected 2 good comments pushed despite 1 failure, got %d", pushed)
		}
		if pulled != 1 {
			t.Fatalf("expected pull phase to still run (1 remote), got %d -- push error blocked the pull", pulled)
		}
	})
}

// Test_SyncLink_pull_sanitizesHTML proves the stored-XSS guard is wired into the
// pull path: a Plane comment carrying a <script> is stored as safe text.
func Test_SyncLink_pull_sanitizesHTML(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t), PlaneAllowPrivateHosts: true})
		fx := newProjectFixture(t, s)
		feat := fx.Features[4]

		malicious := `<p>hi</p><script>alert(document.cookie)</script><img src=x onerror="steal()">`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost {
				w.WriteHeader(201)
				b, _ := json.Marshal(PlaneComment{ID: "x1", CommentHTML: "<p>ok</p>", UpdatedAt: time.Now().UTC()})
				_, _ = w.Write(b)
				return
			}
			b, _ := json.Marshal(planeCommentList{Results: []PlaneComment{{ID: "evil-1", CommentHTML: malicious, UpdatedAt: time.Now().UTC(), Actor: "attacker"}}, NextPageResults: false})
			_, _ = w.Write(b)
		}))
		defer srv.Close()

		_, err := s.SetPlaneConnection(fx.Project.ID, srv.URL, "ws", "key", "")
		mustOK(t, err, "SetPlaneConnection")
		link, err := s.LinkFeatureToPlane(feat.ID, "pp", "wi")
		mustOK(t, err, "link")

		_, pulled, serr := s.SyncLink(ctx, link)
		mustOK(t, serr, "sync")
		if pulled != 1 {
			t.Fatalf("expected 1 pulled, got %d", pulled)
		}
		comments := s.GetFeatureCommentsByFeature(feat.ID)
		if len(comments) != 1 {
			t.Fatalf("expected 1 stored comment, got %d", len(comments))
		}
		post := comments[0].Post
		for _, bad := range []string{"<script", "onerror", "alert(", "<img", "steal()"} {
			if strings.Contains(post, bad) {
				t.Fatalf("stored comment still contains %q (XSS not sanitized): %q", bad, post)
			}
		}
		if !strings.Contains(post, "hi") {
			t.Fatalf("expected visible text 'hi' preserved, got %q", post)
		}
	})
}
