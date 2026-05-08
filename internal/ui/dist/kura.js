/* kura.js — small client-side toolkit for KuraOS admin pages.
 *
 * Loaded once per page from <head>; auto-wires generic behaviors on
 * DOMContentLoaded and exposes APIs on `window.kura` for page scripts.
 *
 *   Auto-wired:
 *     - Body scroll lock while any .modal is visible (MutationObserver
 *       on each modal's `style` attribute).
 *     - <button data-modal-open="modal-id">  → opens that modal.
 *     - <button data-modal-close>             → closes containing modal.
 *     - Click on modal backdrop               → closes that modal.
 *     - ESC                                   → closes topmost visible modal.
 *
 *   API:
 *     kura.modal.open(id) / kura.modal.close(id)
 *         Programmatic open/close. id is the DOM id of the .modal element.
 *
 *     kura.gateForm(form, validate)
 *         Wires submit-button gating on `form`. `validate(form)` must
 *         return an array of human-readable error strings; an empty array
 *         enables the submit button, a non-empty array disables it (via
 *         aria-disabled, not the disabled attribute, so hover tooltips
 *         still fire) and sets its title attribute to a bullet-list of
 *         the errors. The submit button is the first descendant matching
 *         [data-validate-submit] or, failing that, button[type="submit"].
 *         Returns { update } so the caller can force a re-validate from
 *         non-DOM events. Server-side validation is still authoritative;
 *         this is a presentational guard only.
 *
 * Visual SSOT: prototype/claude_design/. CSS for .modal / aria-disabled /
 * disk-picker etc. lives in internal/ui/src/kura.css.
 */
(function (global) {
  "use strict";
  var kura = (global.kura = global.kura || {});

  // ---------- Modal helpers ----------

  function visibleModals() {
    return Array.prototype.filter.call(
      document.querySelectorAll(".modal"),
      function (m) { return m.style.display && m.style.display !== "none"; }
    );
  }

  function syncBodyLock() {
    document.body.classList.toggle("modal-open", visibleModals().length > 0);
  }

  kura.modal = {
    open: function (id) {
      var m = document.getElementById(id);
      if (!m) return;
      m.style.display = "flex";
    },
    close: function (id) {
      var m = document.getElementById(id);
      if (!m) return;
      m.style.display = "none";
    },
    closeTop: function () {
      var v = visibleModals();
      if (v.length) v[v.length - 1].style.display = "none";
    },
  };

  function wireModals() {
    var modals = document.querySelectorAll(".modal");
    if (typeof MutationObserver !== "undefined") {
      var mo = new MutationObserver(syncBodyLock);
      modals.forEach(function (m) {
        mo.observe(m, { attributes: true, attributeFilter: ["style"] });
        // Click on backdrop (the .modal element itself, not its children).
        m.addEventListener("click", function (e) {
          if (e.target === m) m.style.display = "none";
        });
      });
    }

    // Open buttons
    document.querySelectorAll("[data-modal-open]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.preventDefault();
        kura.modal.open(btn.getAttribute("data-modal-open"));
      });
    });

    // Close buttons (close the modal they live inside)
    document.querySelectorAll("[data-modal-close]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.preventDefault();
        var m = btn.closest(".modal");
        if (m) m.style.display = "none";
      });
    });

    // ESC key
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && visibleModals().length) {
        kura.modal.closeTop();
      }
    });

    syncBodyLock();
  }

  // ---------- Form submit-button gating ----------

  kura.gateForm = function (form, validate) {
    if (!form || typeof validate !== "function") return null;
    var submit =
      form.querySelector("[data-validate-submit]") ||
      form.querySelector('button[type="submit"]');
    if (!submit) return null;

    function update() {
      var errs = validate(form) || [];
      if (errs.length === 0) {
        submit.removeAttribute("aria-disabled");
        submit.removeAttribute("title");
      } else {
        submit.setAttribute("aria-disabled", "true");
        submit.setAttribute(
          "title",
          errs
            .map(function (e) { return "• " + e; })
            .join("\n")
        );
      }
    }

    form.addEventListener("change", update);
    form.addEventListener("input", update);
    form.addEventListener("submit", function (e) {
      if (submit.getAttribute("aria-disabled") === "true") {
        e.preventDefault();
        e.stopPropagation();
      }
    });
    update();
    return { update: update };
  };

  // ---------- Boot ----------

  // ---------- Server-rendered tabs with client-side switching ----------
  //
  //   <a class="tab" data-tab-target="volumes" href="?tab=volumes">…</a>
  //   <section data-tab-panel="volumes" data-active="true">…</section>
  //
  // All panels are rendered at once; CSS hides those whose data-active is
  // "false". Clicking a tab swaps data-active across siblings and updates
  // the URL via History.pushState so the back button works and the URL
  // stays bookmarkable. Server-side ?tab= still picks the initial active
  // panel for fresh page loads, so no-JS browsers degrade to full-page
  // reloads gracefully.
  function wireTabs() {
    var tabs = document.querySelectorAll("[data-tab-target]");
    if (!tabs.length) return;

    // Build a map { panelId -> panel-element } once. Panels live anywhere
    // in the DOM (a sibling section, a child of one, …) so we look them up
    // by [data-tab-panel] not by a relative selector.
    var panels = {};
    document.querySelectorAll("[data-tab-panel]").forEach(function (p) {
      panels[p.getAttribute("data-tab-panel")] = p;
    });

    function activate(target, push) {
      // Tab strip: only the matching tab gets data-active="true".
      tabs.forEach(function (t) {
        t.setAttribute(
          "data-active",
          t.getAttribute("data-tab-target") === target ? "true" : "false"
        );
      });
      // Panels: same.
      Object.keys(panels).forEach(function (id) {
        panels[id].setAttribute("data-active", id === target ? "true" : "false");
      });
      if (push && window.history && window.history.pushState) {
        var url = new URL(window.location.href);
        url.searchParams.set("tab", target);
        window.history.pushState({ tab: target }, "", url.toString());
      }
    }

    tabs.forEach(function (tab) {
      tab.addEventListener("click", function (e) {
        e.preventDefault();
        var target = tab.getAttribute("data-tab-target");
        if (target) activate(target, true);
      });
    });

    // Browser back/forward should also swap panels.
    window.addEventListener("popstate", function () {
      var url = new URL(window.location.href);
      var t = url.searchParams.get("tab");
      if (t && panels[t]) activate(t, false);
    });
  }

  // ---------- Radio-driven pane swap ----------
  //
  // Pattern (declarative, no JS per page):
  //
  //   <input type="radio" name="X" value="A" data-radio-shows="pane-a">
  //   <input type="radio" name="X" value="B" data-radio-shows="pane-b">
  //   <div data-testid="pane-a">…</div>
  //   <div data-testid="pane-b">…</div>
  //
  // When the radio changes, the pane whose data-testid matches
  // data-radio-shows of the now-checked radio gets display:""; siblings
  // sharing the same name get display:none. Used by the Share form's
  // "dataset picker / free-text" toggle.
  function wireRadioPanes() {
    var radios = document.querySelectorAll("[data-radio-shows]");
    if (!radios.length) return;

    function panesForName(name) {
      var ps = {};
      document.querySelectorAll('input[type="radio"][name="' + name + '"][data-radio-shows]').forEach(function (r) {
        var id = r.getAttribute("data-radio-shows");
        var el = document.querySelector('[data-testid="' + id + '"]');
        if (el) ps[id] = el;
      });
      return ps;
    }

    function update(radio) {
      var panes = panesForName(radio.name);
      var target = radio.getAttribute("data-radio-shows");
      Object.keys(panes).forEach(function (id) {
        panes[id].style.display = id === target ? "" : "none";
      });
    }

    radios.forEach(function (r) {
      r.addEventListener("change", function () {
        if (r.checked) update(r);
      });
      if (r.checked) update(r);
    });
  }

  function init() {
    wireModals();
    wireTabs();
    wireRadioPanes();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})(window);
