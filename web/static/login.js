import { $, api, bindNumeric, busy, clearFieldErrors, handleError, paintChrome, showFieldError } from "./app.js";

const form = $("#loginForm");
const notice = $("#loginNotice");
const btn = $("#loginBtn");

bindNumeric($("#phone"), 8);
bindNumeric($("#cpr"), 9);

// A birth date can be decades back, so start the picker at a sensible bound.
const dob = $("#dob");
dob.max = new Date().toISOString().slice(0, 10);
dob.min = "1900-01-01";

paintChrome().then((cfg) => {
  if (cfg && cfg.registration_open === false) {
    $("#registerLink").setAttribute("aria-disabled", "true");
    $("#registerHint").textContent = cfg.closed_message || "باب التسجيل مغلق حالياً.";
  }
});

// Already signed in? Go straight through.
api("/api/member/me").then(() => { window.location.replace("/member"); }).catch(() => {});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearFieldErrors(form);
  notice.hidden = true;
  busy(btn, true, "جارٍ التحقق");
  try {
    await api("/api/auth/login", {
      method: "POST",
      body: { phone: $("#phone").value, dob: $("#dob").value, cpr: $("#cpr").value },
    });
    window.location.href = "/member";
  } catch (err) {
    busy(btn, false);
    if (err.field && showFieldError(err.field, err.message, form)) return;
    notice.textContent = err.message;
    notice.hidden = false;
    if (err.status === 401) {
      notice.className = "notice notice--warn";
    } else {
      notice.className = "notice notice--error";
      handleError(err, form);
    }
  }
});
