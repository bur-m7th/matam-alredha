import { $, api, busy, clearFieldErrors, handleError, paintChrome, toast } from "./app.js";
import { collectValues, renderFields } from "./formrender.js";

const form = $("#regForm");
const loading = $("#loadingState");
const closed = $("#closedState");
let definition = null;

paintChrome();

async function load() {
  try {
    definition = await api("/api/public/form");
  } catch (err) {
    loading.querySelector(".empty").textContent = err.message;
    return;
  }
  loading.hidden = true;

  if (!definition.open) {
    $("#closedMessage").textContent = definition.closed_message || "باب التسجيل في الجمعية العمومية مغلق حالياً.";
    closed.hidden = false;
    return;
  }

  document.title = definition.title || document.title;
  $("#formTitle").textContent = definition.title;
  $("#formIntro").textContent = definition.intro || "";
  $("#formIntro").hidden = !definition.intro;
  $("#terms").textContent = definition.terms || "";
  $("#agreeText").textContent = definition.agree_text || "";

  renderFields($("#fields"), definition.fields);
  form.hidden = false;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearFieldErrors(form);

  if (!$("#agree").checked) {
    const holder = form.querySelector('[data-field="agree"]');
    holder.classList.add("has-error");
    holder.querySelector(".field-error").textContent = "يجب الموافقة على شروط العضوية";
    holder.scrollIntoView({ block: "center", behavior: "smooth" });
    return;
  }

  const btn = $("#submitBtn");
  busy(btn, true, "جارٍ الإرسال");
  try {
    await api("/api/public/register", {
      method: "POST",
      body: { values: collectValues($("#fields"), definition.fields), agree: true },
    });
    // Prevents a page refresh from resubmitting the request.
    window.location.replace("/submitted");
  } catch (err) {
    busy(btn, false);
    handleError(err, form);
    if (err.status === 403) {
      toast("تم إغلاق باب التسجيل. الرجاء تحديث الصفحة", "error");
    }
  }
});

// A closing date that arrives while the form is open should not be missed.
setInterval(() => {
  api("/api/public/config")
    .then((cfg) => {
      if (!cfg.registration_open && !form.hidden) {
        form.hidden = true;
        $("#closedMessage").textContent = cfg.closed_message || "باب التسجيل مغلق حالياً.";
        closed.hidden = false;
      }
    })
    .catch(() => {});
}, 120000);

load();
