package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionCheck(t *testing.T) {
	a := &apiServer{sessions: map[string]bool{"valid": true}}
	for _, valid := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/session", nil)
		if valid {
			r.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "valid"})
		}
		w := httptest.NewRecorder()
		a.handler(w, r)
		want := 401
		if valid {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
	}
}
