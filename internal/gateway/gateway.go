package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kuraos-org/kura/i18n"
)

// Deps is what the Gateway needs from the rest of the binary. Keeping the
// surface tiny makes it easy to wire fakes in tests.
type Deps struct {
	Translator *i18n.Translator
	Version    string
	StartedAt  time.Time
	// UIHandler serves /ui/* (admin shell, static assets). Optional so
	// tests that only care about /healthz can skip wiring it.
	UIHandler http.Handler
}

// New returns the http.Handler that fronts every HTTP route the kura binary
// exposes. /healthz lives here; /ui/* is delegated to the UI subtree which is
// owned by internal/ui. Subsequent sprints add /api/v1/... here as well.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz(d))
	if d.UIHandler != nil {
		mux.Handle("/ui/", d.UIHandler)
		mux.Handle("/ui", d.UIHandler)
		// "/" redirects to the admin dashboard so a fresh browser hit lands
		// somewhere meaningful instead of 404. We avoid mounting the UI
		// handler on "/" itself because that would shadow /healthz.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/ui/admin/dashboard", http.StatusFound)
		})
	}
	return mux
}

type healthzResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

func healthz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		body := healthzResponse{
			Status:    d.Translator.T(i18n.MsgSystemHealthy),
			Version:   d.Version,
			StartedAt: d.StartedAt,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}
}
