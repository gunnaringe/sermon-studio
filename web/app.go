package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// A church is a folder in the data dir:
//
//	<data-dir>/<id>/church.json   name, default tags
//	<data-dir>/<id>/Taler/        the sermons
//	<data-dir>/<id>/Innboks/      the inbox
const (
	tenantRoot  = "Taler"
	tenantInbox = "Innboks"
	churchFile  = "church.json"
)

// RootTenant is the id of the church set up with Config.Root and Inbox
// (the --root and --inbox flags): a single church without a church folder,
// so its settings are not saved.
const RootTenant = "default"

// AppConfig sets up all churches.
type AppConfig struct {
	// Base is the setup shared by every church (cache, layout,
	// ffmpeg...). Its Root and Inbox, if set, make the root church.
	Base Config
	// DataDir holds a folder per church. "" = no churches can be added.
	DataDir string
	// OIDC turns on the studio's own login (login.go): a church is then for
	// its members (members.go). nil = no access control (see access.go).
	OIDC *OIDCOptions
	// ChurchHost gives every church a host of its own, "{church}.example.org"
	// (hosts.go); the login is then on the public URL's host. "" = the
	// church is in the address (c/<church>/…), on one host.
	ChurchHost string
	// Share turns on shares (codes for the public upload page); nil = off.
	Share *ShareOptions
	// VisitorRole is, without the login, the role every visitor has in every church (permissions.go): "" =
	// admin. A demo is "demo".
	VisitorRole string
	// ParallelRuns is how many runs (and samples) can work at once, across all churches; 0 = 1. A run uses
	// about one core; more of them wait their turn. One recording is never run twice at once.
	ParallelRuns int
}

// churchConfig is church.json. Everything is optional: a church is a folder
// with a Taler folder in it, named after the folder until it is renamed.
type churchConfig struct {
	Name string `json:"name,omitempty"`
	Meta *Meta  `json:"meta,omitempty"` // default tags; nil = DefaultMeta
	// Processing is the default processing (processing.go); nil = the built-in.
	Processing *processing `json:"processing,omitempty"`
	// EndedSharesDays is how many days a share that has ended is kept before it is removed by itself;
	// nil = defaultEndedSharesDays, 0 = never.
	EndedSharesDays *int `json:"endedSharesDays,omitempty"`
	// Clips are the intro and outro clips (clips.go): the list, the defaults, the pause.
	Clips *clipLibrary `json:"clips,omitempty"`
}

// TenantInfo is a church as listed in the UI.
type TenantInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Logo    string   `json:"logo,omitempty"`    // the church's own logo (see logo.go): its version; "" = none
	Role    string   `json:"role,omitempty"`    // the user's: admin or member (in the church list)
	Actions []string `json:"actions,omitempty"` // what the user may do in it (permissions.go)
	URL     string   `json:"url,omitempty"`     // the church's own address, with church hosts (hosts.go)
	Full    bool     `json:"full,omitempty"`    // it keeps its whole quota (storage.go); told only to its admins
}

type tenant struct {
	id  string
	dir string // the church folder; "" for the root church
	cfg churchConfig
	srv *Server
	api http.Handler

	shares  []*Share // guarded by App.shareMu
	members []member // guarded by App.mu
}

func (t *tenant) info() TenantInfo {
	name := t.cfg.Name
	if name == "" {
		name = t.id
		if t.id == RootTenant {
			name = "Default"
		}
	}
	return TenantInfo{ID: t.id, Name: name, Logo: t.logoVersion()}
}

// App serves several churches (tenants), each with its own date folders,
// inbox and settings. Which one a request is for is the "tenant" query
// parameter or the X-Tenant header; without either it is the first church the
// user sees. Who sees which church is in access.go; every request to a church
// goes through App.tenantFor.
type App struct {
	cfg    AppConfig
	limits *limits

	createMu sync.Mutex // one church is added at a time
	mu       sync.Mutex
	tenants  []*tenant

	share   *shareState  // nil = shares are off
	shareMu sync.Mutex   // every church's shares and uploads in progress
	auth    *auth        // nil = no login of its own
	hosts   *churchHosts // nil = no church hosts
}

// shareState is what the public upload page keeps in memory.
type shareState struct {
	limits *limiter
	busy   map[string]bool // uploads receiving a chunk now
	// sending counts the files a download share is sending now, by share id
	sending map[string]int
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewApp starts the root church (if any) and every church folder in the
// data dir.
func NewApp(cfg AppConfig) (*App, error) {
	if cfg.Base.Logger == nil {
		cfg.Base.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	a := &App{cfg: cfg, limits: newLimits(cfg.ParallelRuns)}
	if cfg.Share != nil {
		a.share = &shareState{limits: newLimiter(cfg.Share.Limits), busy: map[string]bool{}, sending: map[string]int{}, done: make(chan struct{})}
	}
	fail := func(err error) (*App, error) { a.Close(); return nil, err }
	if cfg.VisitorRole != "" {
		if cfg.OIDC != nil {
			return nil, errors.New("a visitor role is for the studio without the login")
		}
		if _, ok := roleActions[cfg.VisitorRole]; !ok {
			return nil, fmt.Errorf("no role %q: the roles are %s, %s and %s", cfg.VisitorRole, RoleAdmin, RoleMember, RoleDemo)
		}
	}
	if cfg.OIDC != nil {
		var err error
		if a.auth, err = newAuth(*cfg.OIDC, cfg.DataDir, cfg.Base.Logger); err != nil {
			return nil, err
		}
	}
	if cfg.ChurchHost != "" {
		h, err := parseChurchHost(cfg.ChurchHost)
		if err == nil {
			err = checkHosts(h, cfg.OIDC)
		}
		if err != nil {
			return nil, err
		}
		a.hosts, a.auth.hosts = h, h
	}
	if cfg.Base.Root != "" {
		if _, err := a.start(RootTenant, "", churchConfig{}); err != nil {
			return fail(fmt.Errorf("--root: %w", err))
		}
	}
	if cfg.DataDir != "" {
		ids, err := churchFolders(cfg.DataDir)
		if err != nil {
			return fail(err)
		}
		for _, id := range ids {
			if reservedID(id) {
				return fail(fmt.Errorf("church folder %q: the name is reserved", id))
			}
			dir := filepath.Join(cfg.DataDir, id)
			cc, err := readChurch(dir)
			if err == nil {
				_, err = a.start(id, dir, cc)
			}
			if err != nil {
				return fail(fmt.Errorf("church %s: %w", id, err))
			}
		}
	}
	if err := a.loadQuotas(); err != nil {
		return fail(err)
	}
	if len(a.tenants) == 0 {
		return nil, errors.New("no churches: set --root, or --data-dir with a folder per church (<id>/Taler)")
	}
	if a.share != nil {
		a.share.wg.Add(1)
		go a.keepSweeping()
	}
	return a, nil
}

// churchFolders lists the folders of dir that are churches: they have a
// church.json or a Taler folder. Sorted, so the first one is always the same.
func churchFolders(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	var ids []string
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		for _, marker := range []string{churchFile, tenantRoot} {
			if _, err := os.Stat(filepath.Join(dir, de.Name(), marker)); err == nil {
				ids = append(ids, de.Name())
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func readChurch(dir string) (churchConfig, error) {
	var cc churchConfig
	b, err := os.ReadFile(filepath.Join(dir, churchFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cc, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &cc)
	}
	if err != nil {
		return cc, fmt.Errorf("%s: %w", churchFile, err)
	}
	return cc, nil
}

// writeChurch saves church.json atomically.
func writeChurch(dir string, cc churchConfig) error {
	b, err := json.MarshalIndent(cc, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, churchFile)
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return normalizer.Rename(tmp, path)
}

// start brings up the Server of one church and adds it to the list.
func (a *App) start(id, dir string, cc churchConfig) (*tenant, error) {
	cfg := a.cfg.Base
	cfg.limits = a.limits
	t := &tenant{id: id, dir: dir, cfg: cc}
	if dir != "" {
		cfg.Root, cfg.Inbox = filepath.Join(dir, tenantRoot), filepath.Join(dir, tenantInbox)
		for _, d := range []string{cfg.Root, cfg.Inbox} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return nil, err
			}
		}
		cfg.Meta = cc.Meta
		cfg.SaveMeta = func(m Meta) error { return a.update(t, func(c *churchConfig) { c.Meta = &m }) }
		cfg.Processing = cc.Processing
		cfg.SaveProcessing = func(p *processing) error { return a.update(t, func(c *churchConfig) { c.Processing = p }) }
		cfg.ClipsDir, cfg.Clips = filepath.Join(dir, clipsDir), cc.Clips
		cfg.SaveClips = func(l clipLibrary) error { return a.update(t, func(c *churchConfig) { c.Clips = &l }) }
	}
	if dir != "" {
		var err error
		if t.shares, err = readShares(dir); err != nil {
			return nil, err
		}
		if t.members, err = readMembers(dir); err != nil {
			return nil, err
		}
	}
	srv, err := New(cfg)
	if err != nil {
		return nil, err
	}
	t.srv, t.api = srv, srv.changesStorage(srv.apiMux())
	a.mu.Lock()
	a.tenants = append(a.tenants, t)
	a.mu.Unlock()
	return t, nil
}

// update changes a church's settings and saves its church.json; if saving
// fails they are left as they were. The root church has no folder: its
// settings stay in memory.
func (a *App) update(t *tenant, change func(*churchConfig)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := t.cfg
	change(&next)
	if t.dir != "" {
		if err := writeChurch(t.dir, next); err != nil {
			return err
		}
	}
	t.cfg = next
	return nil
}

// Busy reports whether stopping now would break something: a church is busy (Server.Busy), or the share
// page is receiving or sending.
func (a *App) Busy() bool {
	a.mu.Lock()
	tenants := append([]*tenant(nil), a.tenants...)
	a.mu.Unlock()
	for _, t := range tenants {
		if t.srv.Busy() {
			return true
		}
	}
	if a.share == nil {
		return false
	}
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	return len(a.share.busy) > 0 || len(a.share.sending) > 0
}

// Close stops every church's background work.
func (a *App) Close() {
	if a.share != nil {
		close(a.share.done)
		a.share.wg.Wait()
	}
	for _, t := range a.tenants {
		if t.srv != nil {
			t.srv.Close()
		}
	}
}

// tenantFor picks the church a request is for: the one it names, else the
// first the user sees. A church the user does not see is "no such church".
func (a *App) tenantFor(r *http.Request) (*tenant, bool) {
	id := r.URL.Query().Get("tenant")
	if id == "" {
		id = r.Header.Get("X-Tenant")
	}
	// With church hosts, the church is the host's: a request naming another
	// is for no church, and so is one on the login host.
	if a.hosts != nil {
		host, ok := a.hostChurch(r)
		if !ok || (id != "" && id != host) {
			return nil, false
		}
		id = host
	}
	for _, t := range a.visible(a.userOf(r)) {
		if id == "" || t.id == id {
			return t, true
		}
	}
	return nil, false
}

// isStart reports whether the request's / is the start (the church picker): everywhere but a church's own host.
func (a *App) isStart(r *http.Request) bool {
	_, church := a.hostChurch(r)
	return !church
}

// Handler is the whole site: the front end and the API of every church.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("GET /", servePages(sub, a.isStart))
	handle(mux, "GET /api/session", a.handleSession)
	// healthz is for the health checks (Kubernetes): open, also with the login.
	handle(mux, "GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	if a.auth != nil {
		handle(mux, "GET /auth/login", a.auth.handleLogin)
		handle(mux, "GET /auth/callback", a.auth.handleCallback)
		handle(mux, "POST /auth/logout", a.auth.handleLogout)
		handle(mux, "GET /auth/logout/callback", a.auth.handleLogoutCallback)
		handle(mux, "GET /api/invite", a.handleInvite)
		handle(mux, "POST /api/invite", a.handleInviteAccept)
		handle(mux, "GET /api/members", a.handleMembers)
		handle(mux, "PUT /api/members/{id}", a.handleMemberRole)
		handle(mux, "POST /api/members/{id}/remove", a.handleMemberRemove)
		handle(mux, "POST /api/members/leave", a.handleLeave)
		handle(mux, "POST /api/invites", a.handleInviteCreate)
		handle(mux, "POST /api/invites/{id}/revoke", a.handleInviteRevoke)
	}
	handle(mux, "GET /api/tenants", a.handleTenants)
	handle(mux, "POST /api/tenants", a.handleTenantCreate)
	handle(mux, "PUT /api/church", a.handleChurchRename)
	handle(mux, "GET /api/logo", a.handleLogo)
	handle(mux, "PUT /api/logo", a.handleLogoPut)
	handle(mux, "DELETE /api/logo", a.handleLogoDelete)
	if a.share != nil {
		handle(mux, "GET /api/shares", a.handleShares)
		handle(mux, "POST /api/shares", a.handleShareCreate)
		handle(mux, "POST /api/shares/{id}/pause", a.handleSharePause)
		handle(mux, "POST /api/shares/{id}/resume", a.handleShareResume)
		handle(mux, "POST /api/shares/{id}/remove", a.handleShareRemove)
		handle(mux, "POST /api/shares/remove-ended", a.handleShareRemoveEnded)
		handle(mux, "PUT /api/shares/ended-days", a.handleEndedSharesDays)
		handle(mux, "GET /api/shares/active", a.handleSharesActive)
	}
	toChurch := func(w http.ResponseWriter, r *http.Request) {
		t, ok := a.tenantFor(r)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("no such church"))
			return
		}
		if a.auth != nil && r.Method == http.MethodGet && r.URL.Path == "/api/config" { // the church is opened
			http.SetCookie(w, a.auth.cookie(churchCookie, t.id, 365*24*time.Hour))
		}
		t.api.ServeHTTP(w, r)
	}
	for _, method := range []string{"GET", "POST", "PUT"} {
		mux.HandleFunc(method+" /api/", toChurch)
	}
	h := a.permit(mux)
	if a.auth != nil {
		h = a.auth.require(a.permit(a.hostGate(mux)))
	}
	// Once there is a login cookie, other sites must not be able to
	// start runs, uploads or imports on the user's behalf.
	return noIndex(http.NewCrossOriginProtection().Handler(h))
}

// noIndex keeps the studio out of search engines: its pages are a login, a
// church's own recordings or a share code's page, none of them for the public.
// It is a header, not robots.txt, so a crawler sees it.
func noIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// handleSession tells the page what it needs before it knows the user has
// a church: where to log out.
func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		u := a.userOf(r)
		writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": "auth/logout", "user": a.auth.userName(u.id)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (a *App) handleTenants(w http.ResponseWriter, r *http.Request) {
	u := a.userOf(r)
	list := []TenantInfo{}
	// default: the host's church; else the one last opened (studio_church), else the first.
	def := ""
	host, _ := a.hostChurch(r)
	last := ""
	if c, err := r.Cookie(churchCookie); err == nil && host == "" {
		last = c.Value
	}
	for _, t := range a.visible(u) {
		i := a.infoOf(t)
		i.Role = a.roleOf(u, t)
		i.Actions = actionsOf(i.Role)
		i.Full = can(i.Role, actChurchSettings) && t.srv.over() // the banner is for those who can make room and see the storage
		list = append(list, i)
		if def == "" || t.id == host || t.id == last {
			def = t.id
		}
	}
	if len(list) == 0 {
		writeError(w, http.StatusForbidden, errNoChurch())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": list, "default": def, "canAdd": a.canAdd(), "shares": a.share != nil,
		"login": a.auth != nil})
}

var reNotID = regexp.MustCompile(`[^a-z0-9]+`)

// tenantID makes a folder-safe id from a church name ("" if it has no letters
// or digits). It may be reserved.
func tenantID(name string) string {
	r := strings.NewReplacer("æ", "ae", "ø", "o", "å", "a", "ä", "a", "ö", "o", "é", "e")
	id := strings.Trim(reNotID.ReplaceAllString(r.Replace(strings.ToLower(name)), "-"), "-")
	return id
}

// churchName checks a church's display name.
func churchName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 80 || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }) {
		return "", errors.New("give the church a name (up to 80 characters)")
	}
	return name, nil
}

// nameTaken reports the church (other than t) that already has this name.
func (a *App) nameTaken(t *tenant, name string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, o := range a.tenants {
		if o != t && strings.EqualFold(o.info().Name, name) {
			return o.info().Name, true
		}
	}
	return "", false
}

// canAdd reports whether churches are added in the studio: only without the
// login (with it, `sermon-studio admin` does that), when visitors are admins, and with a data dir.
func (a *App) canAdd() bool {
	return a.auth == nil && a.visitorRole() == RoleAdmin && a.cfg.DataDir != ""
}

// visitorRole is the role of every visitor without the login.
func (a *App) visitorRole() string {
	if a.cfg.VisitorRole == "" {
		return RoleAdmin
	}
	return a.cfg.VisitorRole
}

func (a *App) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		writeError(w, http.StatusForbidden, errors.New("churches are not added in the studio when it has a login"))
		return
	}
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := a.addChurch("", q.Name)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t.info())
}

// handleChurchRename changes the display name of the selected church.
func (a *App) handleChurchRename(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return
	}
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.renameChurch(t, q.Name); err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t.info())
}
