package web

import (
	"errors"
	"net/http"
	"path/filepath"
)

// Quotas: how much each church may keep (storage.go counts it). They are the site administrator's, in
// <data-dir>/churches.json, set with `sermon-studio admin church quota`; a church's own admins cannot
// change them. A church without one has no limit.
const churchesFile = "churches.json"

// siteChurch is a church in churches.json.
type siteChurch struct {
	Quota int64 `json:"quota,omitempty"` // bytes; 0 = no limit
}

func readSiteChurches(dataDir string) (map[string]siteChurch, error) {
	list := map[string]siteChurch{}
	return list, readJSONFile(filepath.Join(dataDir, churchesFile), &list)
}

// loadQuotas gives every church its quota from churches.json.
func (a *App) loadQuotas() error {
	if a.cfg.DataDir == "" {
		return nil
	}
	list, err := readSiteChurches(a.cfg.DataDir)
	if err != nil {
		return err
	}
	for _, t := range a.tenants {
		t.srv.SetQuota(list[t.id].Quota)
	}
	return nil
}

// setQuota sets a church's quota (0 = no limit) and saves churches.json.
func (a *App) setQuota(t *tenant, bytes int64) error {
	if bytes < 0 {
		return failWith(http.StatusBadRequest, "a quota cannot be below 0")
	}
	a.createMu.Lock() // churches.json is written by one at a time
	defer a.createMu.Unlock()
	list, err := readSiteChurches(a.cfg.DataDir)
	if err != nil {
		return err
	}
	c := list[t.id]
	c.Quota = bytes
	if c == (siteChurch{}) {
		delete(list, t.id)
	} else {
		list[t.id] = c
	}
	if err := writeJSONFile(filepath.Join(a.cfg.DataDir, churchesFile), list); err != nil {
		return err
	}
	t.srv.SetQuota(bytes)
	t.srv.storageChanged() // the answer says what it keeps now
	return nil
}

// adminChurchQuota sets a church's quota: POST /churches/{church}/quota {"quota": bytes}; 0 = no limit.
func (a *App) adminChurchQuota(r *http.Request) (int, any, error) {
	t, err := a.church(r)
	if err != nil {
		return 0, nil, err
	}
	var q struct {
		Quota *int64 `json:"quota"`
	}
	if err := decodeRequest(r, &q); err != nil {
		return 0, nil, statusError{http.StatusBadRequest, err}
	}
	if q.Quota == nil {
		return 0, nil, statusError{http.StatusBadRequest, errors.New("quota is missing")}
	}
	if err := a.setQuota(t, *q.Quota); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, a.adminChurch(t), nil
}
