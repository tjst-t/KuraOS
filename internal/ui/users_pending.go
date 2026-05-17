// users_pending.go: handlers for the 承認待ち (pending-user approval) flow
// introduced in S413bd5-3. Two POST endpoints:
//
//	/ui/admin/users/{id}/approve  — calls engine/system.PromoteFromPending,
//	    returns an htmx fragment containing the one-time password reveal
//	    modal. The fragment is swapped into #pending-action-target by the
//	    htmx hx-target on the approve button.
//	/ui/admin/users/{id}/reject   — calls engine/system.DeleteUser, redirects
//	    back to /ui/admin/users so the page re-renders without the rejected
//	    user row.
//
// The {id} path segment is extracted from the URL by the mux. The mux is
// Go 1.22+ pattern-based so /ui/admin/users/{id}/approve only fires for
// the two-segment sub-path, not for the flat /ui/admin/users/delete etc.
package ui

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/kuraos-org/kura/i18n"
)

// PendingEngine is the slice of engine/system.Engine the pending-approval
// handlers consume. Kept separate from SystemEngine so the wiring diff is
// minimal and the interface boundary stays thin.
type PendingEngine interface {
	PromoteFromPending(ctx context.Context, userID, newRole string) (string, error)
	DeleteUser(ctx context.Context, userID string) error
}

// PendingDeps wires the pending-approval handlers.
type PendingDeps struct {
	Engine PendingEngine
}

// SetPendingHandlers installs the approve and reject handlers.
func (r *Renderer) SetPendingHandlers(d PendingDeps) {
	r.pendingApproveHandler = r.pendingApprove(d)
	r.pendingRejectHandler = r.pendingReject(d)
}

func (r *Renderer) pendingApprove(d PendingDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		// Extract {id} from the path: /ui/admin/users/{id}/approve
		// Go 1.22+ mux populates PathValue; fall back to manual extraction
		// so unit tests using httptest.NewServer still work without a mux.
		userID := req.PathValue("id")
		if userID == "" {
			userID = pathSegment(req.URL.Path, "/approve")
		}
		if userID == "" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrApproveFailed, "missing user id"))
			return
		}
		if err := req.ParseForm(); err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrApproveFailed, err.Error()))
			return
		}
		role := strings.TrimSpace(req.FormValue("role"))
		if role != "admin" && role != "user" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrInvalidRole))
			return
		}

		plaintext, err := d.Engine.PromoteFromPending(req.Context(), userID, role)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "not pending") {
				redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrNotPending))
				return
			}
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrApproveFailed, msg))
			return
		}

		// Return the one-time password reveal fragment. The fragment replaces
		// the modal placeholder injected by the approve button's hx-target.
		frag, err := r.renderApproveSuccess(plaintext)
		if err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrApproveFailed, err.Error()))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(frag)
	})
}

func (r *Renderer) pendingReject(d PendingDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		// Extract {id} from the path: /ui/admin/users/{id}/reject
		userID := req.PathValue("id")
		if userID == "" {
			userID = pathSegment(req.URL.Path, "/reject")
		}
		if userID == "" {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrRejectFailed, "missing user id"))
			return
		}
		if err := d.Engine.DeleteUser(req.Context(), userID); err != nil {
			redirectUsers(w, req, "users", r.tr.T(i18n.MsgUsersErrRejectFailed, err.Error()))
			return
		}
		redirectUsers(w, req, "users", "")
	})
}

// renderApproveSuccess renders the one-time password reveal fragment.
// Returned into #users-modal-target via hx-swap=innerHTML from the approve
// form. Wrapping in .modal (style=display:flex inlined so the kura.js modal
// toggle doesn't have to know about us) makes the existing .modal > .card
// CSS apply — gives the fragment the same centered card + backdrop styling
// the static modals get. Dismiss empties the target so the next approve in
// the same session starts from a clean state.
func (r *Renderer) renderApproveSuccess(plaintext string) ([]byte, error) {
	data := struct {
		Title     string
		PWLabel   string
		Warning   string
		CopyLabel string
		Dismiss   string
		Password  string
	}{
		Title:     r.tr.T(i18n.MsgUsersApproveSuccessTitle),
		PWLabel:   r.tr.T(i18n.MsgUsersApproveSuccessPWLabel),
		Warning:   r.tr.T(i18n.MsgUsersApproveSuccessWarning),
		CopyLabel: r.tr.T(i18n.MsgUsersApproveSuccessCopy),
		Dismiss:   r.tr.T(i18n.MsgUsersApproveSuccessDismiss),
		Password:  plaintext,
	}
	const tpl = `<div class="modal" style="display:flex" data-testid="approve-success-overlay">
  <div class="card" data-testid="approve-success-modal">
    <div class="card-head"><h3 class="card-title" data-testid="approve-success-title">{{.Title}}</h3></div>
    <div class="card-body" style="display:flex;flex-direction:column;gap:14px;">
      <p style="margin:0;font-size:13px;color:#d97706;font-weight:500;" data-testid="approve-success-warning">{{.Warning}}</p>
      <div class="field">
        <label>{{.PWLabel}}</label>
        <div style="display:flex;gap:8px;align-items:center;">
          <code id="approve-pw" class="mono" style="flex:1;padding:8px 10px;background:var(--surface-2);border:1px solid var(--border);border-radius:var(--radius-sm);font-size:13px;word-break:break-all;" data-testid="approve-success-password">{{.Password}}</code>
          <button type="button" class="btn btn-sm" data-testid="approve-success-copy-btn"
            onclick="navigator.clipboard.writeText(document.getElementById('approve-pw').textContent).catch(()=>{})">{{.CopyLabel}}</button>
        </div>
      </div>
    </div>
    <div class="card-foot" style="justify-content:flex-end;">
      <button type="button" class="btn btn-primary" data-testid="approve-success-dismiss-btn"
        onclick="document.getElementById('users-modal-target').innerHTML='';window.location.reload();">{{.Dismiss}}</button>
    </div>
  </div>
</div>`
	t, err := template.New("approve-success").Parse(tpl)
	if err != nil {
		return nil, fmt.Errorf("renderApproveSuccess: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("renderApproveSuccess: execute: %w", err)
	}
	return buf.Bytes(), nil
}

// pathSegment extracts the {id} from a URL path of the form
// /ui/admin/users/{id}/suffix where suffix is the provided trailing segment
// (including the leading slash, e.g. "/approve").
func pathSegment(path, suffix string) string {
	if !strings.HasSuffix(path, suffix) {
		return ""
	}
	trimmed := strings.TrimSuffix(path, suffix)
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return trimmed
	}
	return trimmed[idx+1:]
}

// pendingApproveFragmentURL returns the POST target for the approve button.
func pendingApproveFragmentURL(userID string) string {
	return "/ui/admin/users/" + url.PathEscape(userID) + "/approve"
}

// pendingRejectURL returns the POST target for the reject button.
func pendingRejectURL(userID string) string {
	return "/ui/admin/users/" + url.PathEscape(userID) + "/reject"
}
