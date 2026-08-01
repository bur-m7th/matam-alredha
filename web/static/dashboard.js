/* Pieces shared by the two dashboards. Each dashboard passes its own API
   prefix, so a membership session is never used against an elections route
   and the reverse. */

import { $, $$, api, busy, clearFieldErrors, el, handleError, toast } from "./app.js";

export function setupTabs(onChange) {
  const tabs = $$(".tab");
  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      tabs.forEach((t) => t.setAttribute("aria-selected", String(t === tab)));
      $$("[data-panel]").forEach((p) => (p.hidden = p.dataset.panel !== tab.dataset.tab));
      if (onChange) onChange(tab.dataset.tab);
    });
  });
}

export function setupLoginGate({ prefix, onReady }) {
  const gate = $("#loginGate");
  const dashboard = $("#dashboard");
  const actions = $("#adminActions");
  const notice = $("#loginNotice");

  async function check() {
    try {
      const admin = await api(`${prefix}/session`);
      gate.hidden = true;
      dashboard.hidden = false;
      actions.hidden = false;
      onReady(admin);
    } catch {
      gate.hidden = false;
      dashboard.hidden = true;
      actions.hidden = true;
    }
  }

  $("#loginForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    notice.hidden = true;
    clearFieldErrors(gate);
    const btn = $("#loginBtn");
    busy(btn, true, "جارٍ التحقق");
    try {
      await api(`${prefix}/login`, {
        method: "POST",
        body: { username: $("#username").value, password: $("#password").value },
      });
      $("#password").value = "";
      await check();
    } catch (err) {
      notice.textContent = err.message;
      notice.hidden = false;
    } finally {
      busy(btn, false);
    }
  });

  $("#logoutBtn").addEventListener("click", async () => {
    try { await api(`${prefix}/logout`, { method: "POST" }); } catch { /* leave anyway */ }
    window.location.reload();
  });

  $("#passwordBtn").addEventListener("click", () => openPasswordDialog(prefix));

  check();
  return check;
}

function openPasswordDialog(prefix) {
  const dialog = el("dialog", {},
    el("form", { method: "dialog" },
      el("div", { class: "dialog__head", text: "تغيير كلمة المرور" }),
      el("div", { class: "dialog__body" },
        el("div", { class: "notice notice--error", id: "pwNotice", hidden: true }),
        el("div", { class: "field", "data-field": "old_password" },
          el("label", { class: "label", for: "pwOld", text: "كلمة المرور الحالية" }),
          el("input", { id: "pwOld", type: "password", autocomplete: "current-password" }),
          el("p", { class: "field-error" })
        ),
        el("div", { class: "field", "data-field": "new_password" },
          el("label", { class: "label", for: "pwNew", text: "كلمة المرور الجديدة" }),
          el("input", { id: "pwNew", type: "password", autocomplete: "new-password" }),
          el("p", { class: "help", text: "ثمانية أحرف على الأقل، وتحتوي على أحرف وأرقام" }),
          el("p", { class: "field-error" })
        ),
        el("div", { class: "field", "data-field": "confirm_password" },
          el("label", { class: "label", for: "pwConfirm", text: "تأكيد كلمة المرور الجديدة" }),
          el("input", { id: "pwConfirm", type: "password", autocomplete: "new-password" }),
          el("p", { class: "field-error" })
        )
      ),
      el("div", { class: "dialog__foot" },
        el("button", { class: "btn btn--ghost", type: "button", onclick: () => { dialog.close(); dialog.remove(); } }, "إلغاء"),
        el("button", { class: "btn", type: "button", id: "pwSave" }, "حفظ")
      )
    )
  );
  document.body.append(dialog);
  dialog.showModal();

  dialog.querySelector("#pwSave").addEventListener("click", async () => {
    clearFieldErrors(dialog);
    const notice = dialog.querySelector("#pwNotice");
    notice.hidden = true;
    const btn = dialog.querySelector("#pwSave");
    busy(btn, true, "جارٍ الحفظ");
    try {
      const res = await api(`${prefix}/password`, {
        method: "POST",
        body: {
          old_password: dialog.querySelector("#pwOld").value,
          new_password: dialog.querySelector("#pwNew").value,
          confirm_password: dialog.querySelector("#pwConfirm").value,
        },
      });
      dialog.close();
      dialog.remove();
      toast(res.message, "ok");
      // The change invalidates other sessions; sign out here as well.
      setTimeout(async () => {
        try { await api(`${prefix}/logout`, { method: "POST" }); } catch { /* ignore */ }
        window.location.reload();
      }, 1200);
    } catch (err) {
      busy(btn, false);
      if (!err.field) {
        notice.textContent = err.message;
        notice.hidden = false;
      } else {
        handleError(err, dialog);
      }
    }
  });
}

export function stat(number, label) {
  return el("div", { class: "stat" },
    el("div", { class: "stat__n", text: String(number) }),
    el("div", { class: "stat__l", text: label })
  );
}

export function emptyState(message) {
  return el("div", { class: "card" }, el("p", { class: "empty", text: message }));
}

/* Debounces a search box so typing does not fire a request per keystroke. */
export function onSearch(input, handler, delay = 250) {
  let timer;
  input.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(handler, delay);
  });
}
