package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// handleShareUpdate changes a share: PUT api/shares/{id} with the share as POST api/shares takes it. Its name,
// description and expiry, and what it allows: an upload share's file types and limits; a download share's formats,
// transcript and number of downloads (not its recording or version, which make another share). The code, the
// counts and the log stay. A share that had ended (expired, used up) is open again when the change allows more;
// downloads going on of a format it no longer gives stop.
func (a *App) handleShareUpdate(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	var q shareRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	a.shareMu.Lock()
	i := slices.IndexFunc(t.shares, func(sh *Share) bool { return sh.ID == id })
	var old *Share
	if i >= 0 {
		old = t.shares[i]
	}
	a.shareMu.Unlock()
	if old == nil {
		writeError(w, http.StatusNotFound, errors.New("no such share"))
		return
	}
	now := time.Now()
	label := strings.Join(strings.Fields(q.Label), " ")
	var next *Share
	var err error
	if old.Kind == kindDownload {
		next, err = a.changedDownload(t, old, q, now)
	} else {
		next, err = q.uploadShare(now)
	}
	if err == nil {
		err = checkLabel(label)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	sh := old
	if !slices.Contains(t.shares, sh) {
		writeError(w, http.StatusNotFound, errors.New("no such share")) // removed meanwhile
		return
	}
	before := *sh
	sh.Label, sh.Description, sh.Expires = label, next.Description, nil
	if q.Expires != nil {
		e := q.Expires.UTC()
		sh.Expires = &e
	}
	if sh.Kind == kindDownload {
		sh.Formats, sh.AllFormats, sh.Transcript, sh.MaxDownloads = next.Formats, next.AllFormats, next.Transcript, next.MaxDownloads
		for tid, tk := range sh.Tickets {
			if !sh.gives(tk.Format) {
				delete(sh.Tickets, tid)
			}
		}
	} else {
		sh.Types, sh.AllTypes, sh.MaxFileSize, sh.MaxUploads, sh.MaxTotal = next.Types, next.AllTypes, next.MaxFileSize, next.MaxUploads, next.MaxTotal
	}
	if err := a.saveShares(t); err != nil {
		*sh = before
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("share: %s changed %s share %s (%q)", t.id, sh.view(now).Kind, sh.ID, sh.Label)
	writeJSON(w, http.StatusOK, map[string]any{"share": sh.view(now)})
}

// changedDownload is the download share old with q's formats, transcript, limit and description, checked against
// its recording as it is now (shareable): what it gives must be there.
func (a *App) changedDownload(t *tenant, old *Share, q shareRequest, now time.Time) (*Share, error) {
	var picked []string
	for _, f := range downloadFormats {
		if slices.Contains(q.Formats, f) {
			picked = append(picked, f)
		}
	}
	formats := picked
	if q.AllFormats {
		formats, picked = downloadFormats, nil
	}
	desc := cleanDescription(q.Description)
	switch {
	case !q.AllFormats && (len(picked) == 0 || len(picked) != len(q.Formats)):
		return nil, fmt.Errorf("choose formats from %s", strings.Join(downloadFormats, ", "))
	case q.MaxDownloads < 0:
		return nil, errors.New("limits cannot be negative")
	case q.Expires != nil && !q.Expires.After(now):
		return nil, errors.New("the expiry is in the past")
	}
	if err := checkDescription(desc); err != nil {
		return nil, err
	}
	abs := a.findRecordings(t, old)[old]
	if abs == "" {
		return nil, errors.New("its recording is not there any more")
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		return nil, err
	}
	if err := t.srv.shareable(abs, paths, formats, q.AllFormats, old.SermonOnly, q.Transcript); err != nil {
		return nil, err
	}
	return &Share{Formats: picked, AllFormats: q.AllFormats, Transcript: q.Transcript, MaxDownloads: q.MaxDownloads, Description: desc}, nil
}
