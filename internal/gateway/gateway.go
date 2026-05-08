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
}

// New returns the http.Handler that fronts every HTTP route the kura binary
// exposes. In bootstrap (Sprint S0ff37f) this is just /healthz; subsequent
// sprints add /api/v1/... and /ui/... here.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz(d))
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
