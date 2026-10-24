package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogoutRevokesOnlyCurrentSession(t *testing.T) {
	a := &apiServer{sessions: map[string]time.Time{"current": time.Now().Add(time.Hour), "other": time.Now().Add(time.Hour)}}
	req := httptest.NewRequest("POST", "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "current"})
	w := httptest.NewRecorder()
	a.logout(w, req)
	if w.Code != 200 || !a.sessions["current"].IsZero() || a.sessions["other"].IsZero() {
		t.Fatal("wrong session revoked")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Path != "/" {
		t.Fatal("cookie not cleared")
	}
	w = httptest.NewRecorder()
	if a.auth(w, req) || w.Code != 401 {
		t.Fatal("revoked session still authorized")
	}
	w = httptest.NewRecorder()
	a.logout(w, httptest.NewRequest("GET", "/api/logout", nil))
	if w.Code != 405 {
		t.Fatal("GET logout allowed")
	}
}
