package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A share's description is the church's own: listed in the studio, never on the share page.
func TestShareDescription(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","label":"For the homebound","formats":["mp3"],"description":"  Sent to Kari.\r\nAsk again in May.  "}`)
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if len(list.Shares) != 1 || list.Shares[0].Description != "Sent to Kari.\nAsk again in May." {
		t.Errorf("listed: %+v", list.Shares)
	}
	if w := st.call("GET", "/api/share", code, "1.1.1.1", nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Kari") {
		t.Errorf("the share page has the description: %s", w.Body)
	}
	if w := do(st.studio, "POST", "/api/shares", `{"label":"Up","allTypes":true,"description":"`+strings.Repeat("x", maxDescription+1)+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("too long: %d", w.Code)
	}
}

// Changing a share: its name, description, expiry and what it gives; the code and its downloads stay, and a
// format not made is refused, as when it is made.
func TestShareUpdate(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","label":"Advent","formats":["mp3"],"maxDownloads":1}`)
	if c, _ := st.download(code, "1.1.1.1", "mp3"); c != http.StatusCreated {
		t.Fatalf("download: %d", c)
	}
	if info := st.info(code); info.State != "used up" {
		t.Fatalf("used up: %+v", info)
	}
	id := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", "")).Shares[0].ID
	put := func(body string) *httptest.ResponseRecorder {
		return do(st.studio, "PUT", "/api/shares/"+id, body)
	}
	if w := put(`{"label":"Advent","formats":["mp3","opus"]}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not made yet: OPUS") {
		t.Errorf("a format not made: %d %s", w.Code, w.Body)
	}
	w := put(`{"label":"Advent 2026","description":"For the choir","formats":["mp3","flac"],"maxDownloads":3}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	v := decode[struct{ Share shareView }](t, w).Share
	if v.Label != "Advent 2026" || v.Description != "For the choir" || strings.Join(v.Formats, ",") != "flac,mp3" || v.MaxDownloads != 3 || v.Count != 1 || v.State != "" {
		t.Errorf("changed: %+v", v)
	}
	// The same code, open again, with the FLAC now.
	if info := st.info(code); info.State != "" || len(info.Files) != 2 || *info.DownloadsLeft != 2 {
		t.Errorf("the share page: %+v", info)
	}
	if w := put(`{"label":"Advent","formats":["mp3"],"expires":"2000-01-01T00:00:00Z"}`); w.Code != http.StatusBadRequest {
		t.Errorf("an expiry in the past: %d", w.Code)
	}
	if w := do(st.studio, "PUT", "/api/shares/nope", `{"label":"x","formats":["mp3"]}`); w.Code != http.StatusNotFound {
		t.Errorf("no such share: %d", w.Code)
	}
	// An upload share: its limits.
	_, up := st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	w = do(st.studio, "PUT", "/api/shares/"+up.ID, `{"label":"Sound desk","types":[".wav"],"maxUploads":4,"maxFileSize":2097152}`)
	if w.Code != http.StatusOK {
		t.Fatalf("upload share: %d %s", w.Code, w.Body)
	}
	if v := decode[struct{ Share shareView }](t, w).Share; v.AllTypes || strings.Join(v.Types, ",") != ".wav" || v.MaxUploads != 4 || v.MaxFileSize != 2097152 {
		t.Errorf("upload share: %+v", v)
	}
}
