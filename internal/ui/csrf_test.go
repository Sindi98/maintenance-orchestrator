package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

func TestSameOriginRequest(t *testing.T) {
	cases := []struct {
		name            string
		origin, referer string
		host            string
		want            bool
	}{
		{"no headers (non-browser) allowed", "", "", "dash.example.com", true},
		{"matching origin", "https://dash.example.com", "", "dash.example.com", true},
		{"cross origin blocked", "https://evil.example.com", "", "dash.example.com", false},
		{"referer fallback match", "", "https://dash.example.com/requests/x", "dash.example.com", true},
		{"referer fallback cross", "", "https://evil.example.com/x", "dash.example.com", false},
		{"unparseable origin blocked", "://bad", "", "dash.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/requests/x/cancel", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			if got := sameOriginRequest(r); got != tc.want {
				t.Errorf("sameOriginRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestActionRejectsCrossOrigin(t *testing.T) {
	mr := mkRequest("act", v1alpha1.PhaseAwaitingApproval)
	_, h := newTestServer(t, mr)

	form := url.Values{"gate": {"Drain"}, "by": {"mallory"}}
	req := httptest.NewRequest(http.MethodPost, "/requests/act/cancel", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "dash.example.com"
	req.Header.Set("Origin", "https://evil.example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cancel: code %d, want 403", rec.Code)
	}
}

func TestActionAllowsSameOrigin(t *testing.T) {
	mr := mkRequest("act2", v1alpha1.PhaseAwaitingApproval)
	mr.ObjectMeta.CreationTimestamp = metav1.Now()
	_, h := newTestServer(t, mr)

	req := httptest.NewRequest(http.MethodPost, "/requests/act2/cancel", nil)
	req.Host = "dash.example.com"
	req.Header.Set("Origin", "https://dash.example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin cancel: code %d, want 303", rec.Code)
	}
}
