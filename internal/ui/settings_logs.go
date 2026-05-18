package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/logging"
	"github.com/kuraos-org/kura/i18n"
)

// LogsDeps wires the log store into the Settings Logs tab.
type LogsDeps struct {
	Store *logging.Store
}

// SetLogViewerHandler installs the log viewer handler.
func (r *Renderer) SetLogViewerHandler(deps LogsDeps) {
	r.logViewerHandler = &logViewerHandler{r: r, deps: deps}
}

type logViewerHandler struct {
	r    *Renderer
	deps LogsDeps
}

// logsExtra is passed as PageData.Extra to settings.tmpl for the Logs tab.
type logsExtra struct {
	ActiveTab string
	Source    string
	Level     string
	Search    string
	Entries   []logEntryView
	ErrorMsg  string
}

type logEntryView struct {
	TimeFmt string
	Level   string
	LvlClass string
	Source  string
	Message string
}

func (h *logViewerHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	if path == "/ui/admin/settings/logs/stream" && req.Method == http.MethodGet {
		h.handleSSE(w, req)
		return
	}
	// GET /ui/admin/settings?tab=logs is rendered by the main settings handler.
	http.NotFound(w, req)
}

// buildLogsExtra builds the template data for the Logs tab.
// [AC-Sf92666-3-2]
func (h *logViewerHandler) buildLogsExtra(req *http.Request) *logsExtra {
	source := req.URL.Query().Get("source")
	level := req.URL.Query().Get("level")
	search := req.URL.Query().Get("search")

	extra := &logsExtra{
		ActiveTab: "logs",
		Source:    source,
		Level:     level,
		Search:    search,
	}

	if h.deps.Store == nil {
		return extra
	}

	entries, err := h.deps.Store.Query(req.Context(), logging.Filter{
		Source: source,
		Level:  level,
		Search: search,
	}, 200)
	if err != nil {
		extra.ErrorMsg = h.r.tr.T(i18n.MsgSettingsLogsEmpty)
		return extra
	}

	for _, e := range entries {
		extra.Entries = append(extra.Entries, logEntryView{
			TimeFmt:  e.Timestamp.Local().Format("01/02 15:04:05"),
			Level:    e.Level,
			LvlClass: levelClass(e.Level),
			Source:   e.Source,
			Message:  e.Message,
		})
	}
	return extra
}

// handleSSE streams live log entries via SSE.
// [AC-Sf92666-3-2]
func (h *logViewerHandler) handleSSE(w http.ResponseWriter, req *http.Request) {
	if h.deps.Store == nil {
		http.Error(w, "log store not initialized", http.StatusServiceUnavailable)
		return
	}

	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	source := req.URL.Query().Get("source")
	level := req.URL.Query().Get("level")
	search := req.URL.Query().Get("search")

	ch, unsub := h.deps.Store.Subscribe()
	defer unsub()

	// Send a heartbeat comment to keep the connection open.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := req.Context()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Heartbeat.
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			// Apply filter.
			if source != "" && e.Source != source {
				continue
			}
			if level != "" && e.Level != level {
				continue
			}
			if search != "" && !strings.Contains(e.Message, search) {
				continue
			}
			// Encode as SSE data.
			view := logEntryView{
				TimeFmt:  e.Timestamp.Local().Format("15:04:05"),
				Level:    e.Level,
				LvlClass: levelClass(e.Level),
				Source:   e.Source,
				Message:  e.Message,
			}
			fmt.Fprintf(w, "data: <tr class=\"log-row\">"+
				"<td class=\"td-mono\">%s</td>"+
				"<td><span class=\"badge %s\">%s</span></td>"+
				"<td><span class=\"badge\">%s</span></td>"+
				"<td>%s</td>"+
				"</tr>\n\n",
				view.TimeFmt, view.LvlClass, view.Level, view.Source, view.Message)
			flusher.Flush()
		}
	}
}

func levelClass(level string) string {
	switch level {
	case "error":
		return "crit"
	case "warn", "warning":
		return "warn"
	case "info":
		return "ok"
	default:
		return ""
	}
}
