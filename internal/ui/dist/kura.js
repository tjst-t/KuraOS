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

  function init() {
    wireModals();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})(window);
