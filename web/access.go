package web

import (
	"errors"
	"net/http"
)

// Access control. With the login (AppConfig.OIDC, login.go), a church is for
// its members (members.go). Without it there is no access control: everyone
// sees every church and can add churches (the desktop app, a trusted network).

// reservedID reports ids that cannot be churches: "default" is the church
// of --root, and the site's own host names (hostReserved). Nor can a church
// have a name Windows keeps for a device (nul).
func reservedID(id string) bool {
	return id == RootTenant || hostReserved[id] || windowsDevice(id)
}

// user is who a request is from.
type user struct {
	open bool   // no access control
	id   userID // with the login: who it is
}

// userOf is the user of the request's session; without the login, anyone.
func (a *App) userOf(r *http.Request) user {
	if a.auth == nil {
		return user{open: true}
	}
	id, _ := a.auth.sessionOf(r)
	return user{id: id}
}

// sees reports whether the user may use the church; call with a.mu held.
func (u user) sees(t *tenant) bool {
	if u.open {
		return true
	}
	return u.id != (userID{}) && t.isMember(u.id)
}

// visible lists the churches the user sees, in the order of a.tenants.
func (a *App) visible(u user) []*tenant {
	a.mu.Lock()
	defer a.mu.Unlock()
	var list []*tenant
	for _, t := range a.tenants {
		if u.sees(t) {
			list = append(list, t)
		}
	}
	return list
}

// errNoChurch is what a user without any church is told.
func errNoChurch() error {
	return errors.New("you are not a member of any church yet: ask the church's admin for an invite link")
}
