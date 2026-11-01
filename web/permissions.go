package web

import (
	"errors"
	"net/http"
	"slices"
)

// Permissions. Every request the studio answers is an action, and a role is
// the set of actions its users may take in a church (roleActions). Each route
// is in routeActions, and handle refuses to register one that is not, so a new
// request is closed until it says what it is. permit checks them before any
// handler; the church list tells the page the user's actions (TenantInfo.Actions)
// so it shows only what they may do.
type action string

const (
	actOpen           action = "open"                 // no church: the pages, the session, the login, invites, the church list
	actView           action = "church.view"          // see the church: its recordings, settings and jobs
	actChurchCreate   action = "church.create"        // add a church (without the login only)
	actChurchSettings action = "church.settings"      // name, logo, default tags, processing, how long ended shares are kept
	actChurchClips    action = "church.clips"         // the church's intro and outro clips
	actMembers        action = "members.manage"       // the members and invites
	actUpload         action = "recording.upload"     // send a file: a recording, a sermon's own intro or outro
	actEdit           action = "recording.edit"       // cut, tag, rename, hear a sample
	actMake           action = "recording.make"       // make the files, and stop a run
	actTranscribe     action = "recording.transcribe" // make a transcript, stop or delete one
	actDownload       action = "recording.download"   // listen to and download the files
	actDelete         action = "recording.delete"     // delete files, originals and recordings; the trash
	actShares         action = "shares.manage"        // the church's shares
)

// RoleDemo is a demo's visitor: they edit the recording that is there and make its files.
const RoleDemo = "demo"

// roleActions is what each role may do in a church. Adding a church is no church's: permit lets only the
// user without the login do it, when visitors are admins (AppConfig.VisitorRole).
var roleActions = map[string][]action{
	RoleAdmin:  {actView, actChurchSettings, actChurchClips, actMembers, actUpload, actEdit, actMake, actTranscribe, actDownload, actDelete, actShares},
	RoleMember: {actView, actUpload, actEdit, actMake, actTranscribe, actDownload, actDelete, actShares},
	RoleDemo:   {actView, actEdit, actMake, actDownload},
}

// routeActions is the action of every route of the studio (App.Handler and Server.apiMux).
var routeActions = map[string]action{
	"GET /":                     actOpen,
	"GET /healthz":              actOpen,
	"GET /api/session":          actOpen,
	"GET /auth/login":           actOpen,
	"GET /auth/callback":        actOpen,
	"POST /auth/logout":         actOpen,
	"GET /auth/logout/callback": actOpen,
	"GET /api/invite":           actOpen,
	"POST /api/invite":          actOpen,
	"GET /api/tenants":          actOpen,
	"POST /api/tenants":         actChurchCreate,

	"POST /api/members/leave":       actView,
	"GET /api/members":              actMembers,
	"PUT /api/members/{id}":         actMembers,
	"POST /api/members/{id}/remove": actMembers,
	"POST /api/invites":             actMembers,
	"POST /api/invites/{id}/revoke": actMembers,

	"GET /api/logo":                 actView,
	"PUT /api/church":               actChurchSettings,
	"PUT /api/logo":                 actChurchSettings,
	"DELETE /api/logo":              actChurchSettings,
	"GET /api/settings":             actView,
	"PUT /api/settings":             actChurchSettings,
	"PUT /api/processing":           actChurchSettings,
	"GET /api/transcription":        actView,
	"PUT /api/transcription":        actChurchSettings,
	"POST /api/transcript":          actTranscribe,
	"POST /api/transcript/cancel":   actTranscribe,
	"POST /api/transcript/remove":   actTranscribe,
	"PUT /api/shares/ended-days":    actChurchSettings,
	"GET /api/shares":               actShares,
	"POST /api/shares":              actShares,
	"POST /api/shares/{id}/pause":   actShares,
	"POST /api/shares/{id}/resume":  actShares,
	"POST /api/shares/{id}/remove":  actShares,
	"POST /api/shares/remove-ended": actShares,
	"GET /api/shares/active":        actShares,

	"GET /api/clips":                actView,
	"GET /api/clips/{id}/audio":     actView,
	"POST /api/clips":               actChurchClips,
	"PUT /api/clips/defaults":       actChurchClips,
	"PUT /api/clips/{id}":           actChurchClips,
	"POST /api/clips/{id}/replace":  actChurchClips,
	"POST /api/clips/{id}/delete":   actChurchClips,
	"POST /api/sermon/clip/add":     actChurchClips,
	"GET /api/sermon/clips":         actView,
	"GET /api/sermon/clip/audio":    actView,
	"POST /api/sermon/clip":         actUpload, // a file of its own: an upload
	"POST /api/sermon/clip/dismiss": actEdit,

	"GET /api/config":           actView,
	"GET /api/folders":          actView,
	"GET /api/folder":           actView,
	"GET /api/recordings":       actView,
	"GET /api/recording":        actView,
	"GET /api/analysis":         actView,
	"POST /api/analysis":        actView,
	"GET /api/peaks":            actView,
	"GET /api/preview":          actView,
	"POST /api/meta/preview":    actView,
	"POST /api/check":           actView,
	"GET /api/jobs/{id}":        actView,
	"GET /api/jobs/{id}/events": actView,
	"GET /api/trash":            actView,
	"GET /api/storage":          actChurchSettings, // Settings → Storage is the admins'

	"POST /api/upload":                     actUpload,
	"PUT /api/labels":                      actEdit,
	"PUT /api/sermon":                      actEdit,
	"POST /api/sermon/save":                actEdit,
	"POST /api/sermon/revert":              actEdit,
	"POST /api/rename":                     actEdit,
	"POST /api/sample":                     actEdit,
	"POST /api/sample/join":                actEdit,
	"POST /api/run":                        actMake,
	"POST /api/make":                       actMake,
	"POST /api/jobs/{id}/cancel":           actMake,
	"GET /api/file":                        actDownload,
	"POST /api/outputs/remove":             actDelete,
	"POST /api/recordings/delete":          actDelete,
	"POST /api/recordings/delete-original": actDelete,
	"POST /api/trash/restore":              actDelete,
	"POST /api/trash/remove":               actDelete,
}

// actionMux finds a request's route in routeActions. Under /api/, a request no route has is refused.
var actionMux = func() *http.ServeMux {
	m := http.NewServeMux()
	for p := range routeActions {
		m.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		m.HandleFunc(method+" /api/", func(http.ResponseWriter, *http.Request) {})
	}
	return m
}()

// handle registers a route, which must have its action in routeActions.
func handle(mux *http.ServeMux, pattern string, fn http.HandlerFunc) {
	if _, ok := routeActions[pattern]; !ok {
		panic("web: the route " + pattern + " has no action in routeActions")
	}
	mux.HandleFunc(pattern, fn)
}

// actionOf is the request's action; ok is false for a request the studio has no route for.
func actionOf(r *http.Request) (action, bool) {
	_, pattern := actionMux.Handler(r)
	act, ok := routeActions[pattern]
	return act, ok
}

// can reports whether a role may take an action.
func can(role string, act action) bool { return slices.Contains(roleActions[role], act) }

// actionsOf lists what a role may do, for the page.
func actionsOf(role string) []string {
	var out []string
	for _, act := range roleActions[role] {
		out = append(out, string(act))
	}
	return out
}

// permit lets a request through only when the user may take its action in its church. A request for a
// church the user does not see goes on, and is answered as one for no church.
func (a *App) permit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		act, ok := actionOf(r)
		switch {
		case !ok:
			writeError(w, http.StatusNotFound, errors.New("no such request"))
			return
		case act == actOpen:
		case act == actChurchCreate:
			if !a.userOf(r).open { // with the login, `sermon-studio admin` adds churches
				writeError(w, http.StatusForbidden, errors.New("churches are not added in the studio when it has a login"))
				return
			}
			if a.visitorRole() != RoleAdmin {
				writeError(w, http.StatusForbidden, refusal(act))
				return
			}
		default:
			if t, found := a.tenantFor(r); found {
				if role := a.roleOf(a.userOf(r), t); !can(role, act) {
					writeError(w, http.StatusForbidden, refusal(act))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// refusal says why a request is refused: what only admins may do, or what the user's role does not allow.
func refusal(act action) error {
	if !can(RoleMember, act) {
		return errors.New("only the church's admins can change this")
	}
	return errors.New("you may not do this in this church")
}
