package web

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Every role's actions are actions a route has, and every route's action is one a role can have.
func TestPermissionTable(t *testing.T) {
	used := map[action]bool{}
	for p, act := range routeActions {
		used[act] = true
		if act != actOpen && act != actChurchCreate && !can(RoleAdmin, act) {
			t.Errorf("%s: %s, which no admin has", p, act)
		}
	}
	for role, acts := range roleActions {
		for _, act := range acts {
			if !used[act] {
				t.Errorf("%s: %s, which no route has", role, act)
			}
		}
		if role != RoleAdmin && slices.ContainsFunc(acts, func(act action) bool { return act == actMembers || act == actChurchSettings }) {
			t.Errorf("%s may change the church or its members", role)
		}
	}
}

// A route without an action is not registered.
func TestRouteNeedsAction(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registered")
		}
	}()
	handle(http.NewServeMux(), "POST /api/no-such", func(http.ResponseWriter, *http.Request) {})
}

// A request under /api/ that no route has is refused, with and without the login.
func TestUnknownRequestRefused(t *testing.T) {
	_, h, _, _ := newTestApp(t)
	for _, method := range []string{"GET", "POST", "PATCH"} {
		if w := do(h, method, "/api/no-such", ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, w.Code)
		}
	}
}

// A demo's visitor edits and makes the files, and does nothing else: no upload, no deleting, no
// settings, no clips, no shares, no members.
func TestDemoRole(t *testing.T) {
	l := newLoginApp(t)
	admin := l.loginAs("admin-1")
	token, _ := CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	l.join(admin, token)
	visitor := l.loginAs("visitor")
	token, _ = CreateInvite(l.data, "kirke-a", RoleMember, time.Hour, "admin cli")
	l.join(visitor, token)
	err := l.app.changeMembers(l.app.tenants[slices.IndexFunc(l.app.tenants, func(t *tenant) bool { return t.id == "kirke-a" })],
		func(list []member) ([]member, error) {
			for i := range list {
				if list[i].userID.Subject == "visitor" {
					list[i].Role = RoleDemo
				}
			}
			return list, nil
		})
	if err != nil {
		t.Fatal(err)
	}

	got := decode[struct{ Tenants []TenantInfo }](t, l.do("GET", "/api/tenants", "", visitor)).Tenants
	if len(got) != 1 || !slices.Equal(got[0].Actions, []string{"church.view", "recording.edit", "recording.make", "recording.download"}) {
		t.Errorf("churches: %+v", got)
	}
	for _, req := range [][3]string{{"GET", "/api/config", ""}, {"GET", "/api/recordings", ""}, {"GET", "/api/folders", ""}} {
		if w := l.do(req[0], req[1]+"?tenant=kirke-a", req[2], visitor); w.Code != http.StatusOK {
			t.Errorf("%s %s: %d %s", req[0], req[1], w.Code, w.Body.String())
		}
	}
	for _, req := range [][3]string{
		{"POST", "/api/upload", ""}, {"POST", "/api/sermon/clip?which=intro", ""}, {"POST", "/api/recordings/delete", `{}`}, {"POST", "/api/trash/remove", `{}`},
		{"POST", "/api/outputs/remove", `{}`}, {"PUT", "/api/settings", `{}`}, {"POST", "/api/clips/x/delete", ""},
		{"GET", "/api/members", ""}, {"POST", "/api/invites", `{"role":"member","days":1}`},
	} {
		if w := l.do(req[0], req[1]+"?tenant=kirke-a", req[2], visitor); w.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", req[0], req[1], w.Code)
		}
	}
	// Editing goes past the check (and fails on the folder that is not there).
	if w := l.do("PUT", "/api/sermon?tenant=kirke-a&path=2026/2026-10-04", `{}`, visitor); w.Code == http.StatusForbidden {
		t.Errorf("editing: %d %s", w.Code, w.Body.String())
	}
}

// Without the login, --visitor-role gives every visitor a role: a demo's visitor edits, and adds no churches.
func TestVisitorRole(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "demo-church", "Taler"), 0o755)
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data,
		VisitorRole: RoleDemo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	h := a.Handler()
	got := decode[struct {
		Tenants []TenantInfo
		CanAdd  bool
	}](t, do(h, "GET", "/api/tenants", ""))
	if got.CanAdd || len(got.Tenants) != 1 || got.Tenants[0].Role != RoleDemo || slices.Contains(got.Tenants[0].Actions, "recording.upload") {
		t.Errorf("churches: %+v", got)
	}
	for _, req := range [][3]string{
		{"POST", "/api/tenants", `{"name":"Mine"}`}, {"POST", "/api/upload", ""}, {"POST", "/api/sermon/clip?which=intro", ""},
		{"POST", "/api/recordings/delete", `{}`}, {"PUT", "/api/settings", `{}`},
	} {
		if w := do(h, req[0], req[1], req[2]); w.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", req[0], req[1], w.Code)
		}
	}
	if w := do(h, "GET", "/api/recordings", ""); w.Code != http.StatusOK {
		t.Errorf("recordings: %d %s", w.Code, w.Body.String())
	}

	for _, cfg := range []AppConfig{
		{DataDir: data, VisitorRole: "guest"},
		{DataDir: data, VisitorRole: RoleDemo, OIDC: &OIDCOptions{Issuer: "https://id.example", PublicURL: "https://studio.example/"}},
	} {
		cfg.Base = Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}
		if a, err := NewApp(cfg); err == nil {
			a.Close()
			t.Errorf("%q, login %v: started", cfg.VisitorRole, cfg.OIDC != nil)
		}
	}
}
