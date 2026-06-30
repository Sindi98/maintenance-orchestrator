package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

//go:embed web/templates/*.html web/templates/fragments/*.html
var templatesFS embed.FS

//go:embed web/static/*
var staticFS embed.FS

// staticRoot is the embedded static dir rooted at web/static.
var staticRoot fs.FS

// fullPages are rendered wrapped in the layout; pages holds one parsed set each.
var (
	pages     = map[string]*template.Template{}
	fragments *template.Template
)

func init() {
	sub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		panic(err)
	}
	staticRoot = sub

	for _, p := range []string{"list.html", "detail.html", "new.html", "policies.html"} {
		t := template.New("layout.html").Funcs(funcs)
		template.Must(t.ParseFS(templatesFS,
			"web/templates/layout.html",
			"web/templates/fragments/*.html",
			"web/templates/"+p))
		pages[p] = t
	}
	fragments = template.Must(template.New("").Funcs(funcs).ParseFS(templatesFS, "web/templates/fragments/*.html"))
}

// funcs are the template helpers (badge classes, relative time, formatting).
var funcs = template.FuncMap{
	"phaseClass":     phaseClass,
	"nodePhaseClass": nodePhaseClass,
	"checkClass":     checkClass,
	"ago":            ago,
	"agoPtr":         agoPtr,
	"lower":          strings.ToLower,
	"join":           strings.Join,
	"pct":            pct,
	"isTerminal":     isTerminal,
	"canPause":       canPause,
	"list":           func(s ...string) []string { return s },
}

func isTerminal(p v1alpha1.Phase) bool {
	switch p {
	case v1alpha1.PhaseCompleted, v1alpha1.PhaseFailed, v1alpha1.PhaseCancelled:
		return true
	default:
		return false
	}
}

func canPause(p v1alpha1.Phase) bool {
	switch p {
	case v1alpha1.PhasePlanned, v1alpha1.PhaseExecuting, v1alpha1.PhaseBlocked, v1alpha1.PhaseAwaitingApproval:
		return true
	default:
		return false
	}
}

func (s *Server) renderPage(w http.ResponseWriter, page string, data any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log.Error(err, "render page", "page", page)
	}
}

func (s *Server) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := fragments.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error(err, "render fragment", "fragment", name)
	}
}

func phaseClass(p v1alpha1.Phase) string {
	switch p {
	case v1alpha1.PhaseCompleted:
		return "ok"
	case v1alpha1.PhaseFailed, v1alpha1.PhaseBlocked:
		return "bad"
	case v1alpha1.PhaseExecuting, v1alpha1.PhasePlanned, v1alpha1.PhaseValidating:
		return "busy"
	case v1alpha1.PhaseAwaitingApproval, v1alpha1.PhasePaused:
		return "wait"
	case v1alpha1.PhaseCancelled:
		return "muted"
	default:
		return "muted"
	}
}

func nodePhaseClass(p v1alpha1.NodePhase) string {
	switch p {
	case v1alpha1.NodeCompleted:
		return "ok"
	case v1alpha1.NodeFailed, v1alpha1.NodeBlocked:
		return "bad"
	case v1alpha1.NodeSkipped:
		return "muted"
	case v1alpha1.NodePending:
		return "wait"
	default:
		return "busy"
	}
}

func checkClass(s v1alpha1.CheckStatus) string {
	switch s {
	case v1alpha1.CheckPass:
		return "ok"
	case v1alpha1.CheckWarn:
		return "wait"
	case v1alpha1.CheckFail:
		return "bad"
	default:
		return "muted"
	}
}

func ago(t metav1.Time) string {
	if t.IsZero() {
		return "-"
	}
	return humanizeDuration(time.Since(t.Time))
}

func agoPtr(t *metav1.Time) string {
	if t == nil {
		return "-"
	}
	return ago(*t)
}

func pct(done, total int32) int {
	if total <= 0 {
		return 0
	}
	return int(done * 100 / total)
}

func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
