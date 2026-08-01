import {
  $, api, busy, clear, clearFieldErrors, confirmAction, el,
  handleError, isoToDisplay, paintChrome, stampToDisplay, toast,
} from "./app.js";
import { emptyState, onSearch, setupLoginGate, setupTabs, stat } from "./dashboard.js";
import { collectValues, renderFields } from "./formrender.js";

const PREFIX = "/api/admin";
let formDefinition = { fields: [] };

paintChrome();
setupTabs((tab) => {
  if (tab === "requests") loadRequests();
  if (tab === "members") loadMembers();
  if (tab === "form") loadForm();
  if (tab === "settings") loadOverview();
});

setupLoginGate({
  prefix: PREFIX,
  onReady: () => { loadOverview(); loadRequests(); loadForm(); },
});

/* ------------------------------------------------------------ overview */

async function loadOverview() {
  let data;
  try { data = await api(`${PREFIX}/overview`); } catch (err) { handleError(err); return; }

  clear($("#stats")).append(
    stat(data.pending, "طلبات قيد المراجعة"),
    stat(data.members, "عضو معتمد"),
    stat(data.rejected, "طلب مرفوض"),
    stat(data.registration_open ? "مفتوح" : "مغلق", "باب التسجيل")
  );

  const state = $("#regState");
  if (data.registration_open) {
    state.className = "notice notice--ok";
    state.textContent = data.close_at_display
      ? `التسجيل مفتوح، ويغلق تلقائياً في ${data.close_at_display}`
      : "التسجيل مفتوح ولا يوجد موعد إغلاق مجدول";
  } else {
    state.className = "notice notice--warn";
    state.textContent = "التسجيل مغلق حالياً";
  }
  $("#closeAt").value = data.close_at || "";
  $("#closedMsg").value = data.closed_message || "";

  const audit = clear($("#auditList"));
  if (!data.recent || data.recent.length === 0) {
    audit.append(el("p", { class: "empty", text: "لا توجد إجراءات مسجلة" }));
  } else {
    for (const entry of data.recent) {
      audit.append(el("div", { class: "fieldrow" },
        el("div", { class: "fieldrow__main" },
          el("div", { class: "fieldrow__label", text: actionLabel(entry.action) }),
          el("div", { class: "fieldrow__key", text: `${entry.actor} - ${stampToDisplay(entry.created_at)}` })
        ),
        entry.details ? el("div", { class: "help", text: entry.details }) : null
      ));
    }
  }
}

const ACTIONS = {
  approve_application: "اعتماد طلب عضوية",
  reject_application: "رفض طلب عضوية",
  delete_application: "حذف طلب",
  delete_user: "حذف عضو",
  update_user: "تعديل بيانات عضو",
  seed_members: "استيراد كشف الأعضاء",
  export: "تصدير كشف الأعضاء",
  registration: "تغيير حالة التسجيل",
  update_form_settings: "تعديل نصوص الاستمارة",
  add_field: "إضافة حقل",
  update_field: "تعديل حقل",
  delete_field: "حذف حقل",
  change_password: "تغيير كلمة المرور",
  login: "تسجيل دخول",
};
const actionLabel = (a) => ACTIONS[a] || a;

/* ------------------------------------------------------------ requests */

onSearch($("#reqSearch"), loadRequests);
$("#reqStatus").addEventListener("change", loadRequests);

async function loadRequests() {
  const list = clear($("#requestsList"));
  const params = new URLSearchParams({ status: $("#reqStatus").value, q: $("#reqSearch").value });
  let data;
  try { data = await api(`${PREFIX}/applications?${params}`); } catch (err) { handleError(err); return; }

  if (data.applications.length === 0) {
    list.append(emptyState("لا توجد طلبات مطابقة"));
    return;
  }
  for (const app of data.applications) list.append(requestCard(app));
}

const STATUS_LABEL = { pending: "قيد المراجعة", approved: "معتمد", rejected: "مرفوض" };

function requestCard(app) {
  const details = el("dl", { class: "detail-grid" },
    row("الرقم الشخصي", app.cpr, true),
    row("تاريخ الميلاد", isoToDisplay(app.dob)),
    row("رقم التواصل", app.phone, true),
    app.email ? row("البريد الإلكتروني", app.email, true) : null,
    row("العنوان", `منزل ${app.house} - طريق ${app.road} - مجمع ${app.block}`),
    row("العمل التطوعي", app.volunteer ? `نعم - ${app.volunteer_field}` : "لا"),
    row("منتسب لمؤسسة أخرى", app.affiliated ? `نعم - ${app.affiliation}` : "لا"),
    ...Object.entries(app.extra || {}).map(([k, v]) => row(labelForKey(k), v)),
    row("تاريخ الطلب", stampToDisplay(app.created_at)),
    app.reject_reason ? row("سبب الرفض", app.reject_reason) : null
  );

  const actions = el("div", { class: "btn-row btn-row--end", style: "margin-top:1rem" });
  if (app.status === "pending") {
    actions.append(
      el("button", { class: "btn btn--ghost btn--sm", type: "button", onclick: () => reject(app) }, "رفض"),
      el("button", { class: "btn btn--sm", type: "button", onclick: (e) => approve(app, e.currentTarget) }, "اعتماد العضوية")
    );
  } else if (app.status === "rejected") {
    actions.append(el("button", { class: "btn btn--danger btn--sm", type: "button", onclick: () => removeRequest(app) }, "حذف الطلب"));
  }

  return el("div", { class: "card" },
    el("div", { style: "display:flex;gap:0.75rem;align-items:baseline;flex-wrap:wrap" },
      el("h2", { text: app.name, style: "margin:0" }),
      el("span", { class: `pill pill--${app.status}`, text: STATUS_LABEL[app.status] })
    ),
    el("div", { style: "margin-top:0.75rem" }, details),
    actions
  );
}

function row(term, value, ltr = false) {
  return el("div", { style: "display:contents" },
    el("dt", { text: term }),
    el("dd", { text: value || "-", class: ltr ? "num" : "" })
  );
}

function labelForKey(key) {
  const found = (formDefinition.fields || []).find((f) => f.key === key);
  return found ? found.label : key;
}

async function approve(app, btn) {
  busy(btn, true, "جارٍ الاعتماد");
  try {
    const res = await api(`${PREFIX}/applications/${app.id}/approve`, { method: "POST" });
    toast(res.message, "ok");
    loadRequests();
    loadOverview();
  } catch (err) {
    busy(btn, false);
    handleError(err);
  }
}

async function reject(app) {
  const reason = await promptText("سبب الرفض (اختياري)", "رفض الطلب");
  if (reason === null) return;
  try {
    const res = await api(`${PREFIX}/applications/${app.id}/reject`, { method: "POST", body: { reason } });
    toast(res.message, "ok");
    loadRequests();
    loadOverview();
  } catch (err) { handleError(err); }
}

async function removeRequest(app) {
  if (!await confirmAction(`سيتم حذف طلب ${app.name} نهائياً.`, "حذف")) return;
  try {
    await api(`${PREFIX}/applications/${app.id}`, { method: "DELETE" });
    toast("تم حذف الطلب", "ok");
    loadRequests();
    loadOverview();
  } catch (err) { handleError(err); }
}

/* ------------------------------------------------------------ members */

onSearch($("#memSearch"), loadMembers);

async function loadMembers() {
  const list = clear($("#membersList"));
  let data;
  try {
    data = await api(`${PREFIX}/members?q=${encodeURIComponent($("#memSearch").value)}`);
  } catch (err) { handleError(err); return; }

  if (data.members.length === 0) {
    list.append(emptyState("لا يوجد أعضاء مطابقون"));
    return;
  }

  const body = el("tbody");
  data.members.forEach((m, i) => {
    body.append(el("tr", {},
      el("td", { class: "num", text: String(i + 1) }),
      el("td", { text: m.name }),
      el("td", { class: "num", text: m.cpr }),
      el("td", { class: "num", text: isoToDisplay(m.dob) }),
      el("td", { class: "num", text: m.phone }),
      el("td", { text: `${m.house} / ${m.road} / ${m.block}` }),
      el("td", {},
        el("div", { class: "btn-row" },
          el("button", { class: "btn btn--ghost btn--sm", type: "button", onclick: () => editMember(m) }, "تعديل"),
          el("button", { class: "btn btn--danger btn--sm", type: "button", onclick: () => removeMember(m) }, "حذف")
        )
      )
    ));
  });

  list.append(
    el("p", { class: "help", text: `عدد الأعضاء: ${data.members.length}`, style: "margin-bottom:0.5rem" }),
    el("div", { class: "table-wrap" },
      el("table", {},
        el("thead", {}, el("tr", {},
          el("th", { class: "num", text: "#" }), el("th", { text: "الاسم الرباعي" }),
          el("th", { text: "الرقم الشخصي" }), el("th", { text: "تاريخ الميلاد" }),
          el("th", { text: "رقم التواصل" }), el("th", { text: "المنزل / الطريق / المجمع" }),
          el("th", { text: "إجراءات" })
        )),
        body
      )
    )
  );
}

async function removeMember(member) {
  const confirmed = await confirmAction(
    `سيتم حذف ${member.name} من قاعدة البيانات ومن كشف الأعضاء، مع أي صوت سجله. لا يمكن التراجع.`,
    "حذف نهائياً"
  );
  if (!confirmed) return;
  try {
    const res = await api(`${PREFIX}/members/${member.id}`, { method: "DELETE" });
    toast(res.message, "ok");
    loadMembers();
    loadOverview();
  } catch (err) { handleError(err); }
}

function editMember(member) {
  const values = {
    ...member,
    volunteer: member.volunteer ? "yes" : "no",
    affiliated: member.affiliated ? "yes" : "no",
    dob: member.dob,
    ...(member.extra || {}),
  };
  values.volunteer_field = member.volunteer_field;
  values.affiliation = member.affiliation;

  const holder = el("div", { id: "editFields" });
  const dialog = el("dialog", {},
    el("div", { class: "dialog__head", text: "تعديل بيانات العضو" }),
    el("div", { class: "dialog__body" }, holder),
    el("div", { class: "dialog__foot" },
      el("button", { class: "btn btn--ghost", type: "button", onclick: () => { dialog.close(); dialog.remove(); } }, "إلغاء"),
      el("button", { class: "btn", type: "button", id: "saveMember" }, "حفظ")
    )
  );
  document.body.append(dialog);
  renderFields(holder, formDefinition.fields, values);
  dialog.showModal();

  dialog.querySelector("#saveMember").addEventListener("click", async (event) => {
    clearFieldErrors(dialog);
    const btn = event.currentTarget;
    busy(btn, true, "جارٍ الحفظ");
    try {
      const res = await api(`${PREFIX}/members/${member.id}`, {
        method: "PUT",
        body: { values: collectValues(holder, formDefinition.fields), agree: true },
      });
      dialog.close();
      dialog.remove();
      toast(res.message, "ok");
      loadMembers();
    } catch (err) {
      busy(btn, false);
      handleError(err, dialog);
    }
  });
}

/* ------------------------------------------------------------ form editor */

async function loadForm() {
  try { formDefinition = await api(`${PREFIX}/form`); } catch (err) { handleError(err); return; }

  $("#fsTitle").value = formDefinition.title || "";
  $("#fsIntro").value = formDefinition.intro || "";
  $("#fsTerms").value = formDefinition.terms || "";
  $("#fsAgree").value = formDefinition.agree_text || "";
  $("#fsSuccess").value = formDefinition.success_message || "";

  const list = clear($("#fieldsList"));
  formDefinition.fields.forEach((field, index) => list.append(fieldRow(field, index)));
}

const TYPE_LABEL = {
  text: "نص", textarea: "نص طويل", number: "رقم", date: "تاريخ", email: "بريد إلكتروني",
  phone: "رقم هاتف", cpr: "رقم شخصي", arabic_name: "اسم عربي", yesno: "نعم / لا", select: "قائمة اختيار",
};

function fieldRow(field, index) {
  const controls = el("div", { class: "fieldrow__ctrl" });

  if (!field.locked) {
    controls.append(
      el("button", {
        class: "btn btn--ghost btn--sm", type: "button",
        title: field.enabled ? "إخفاء من الاستمارة" : "إظهار في الاستمارة",
        onclick: () => saveField({ ...field, enabled: !field.enabled }),
      }, field.enabled ? "مفعّل" : "مخفي")
    );
  }
  controls.append(
    el("button", { class: "btn btn--ghost btn--sm", type: "button", onclick: () => editField(field) }, "تحرير"),
    index > 0 ? el("button", { class: "btn btn--ghost btn--sm", type: "button", title: "تحريك لأعلى", onclick: () => move(index, -1) }, "\u2191") : null,
    index < formDefinition.fields.length - 1 ? el("button", { class: "btn btn--ghost btn--sm", type: "button", title: "تحريك لأسفل", onclick: () => move(index, 1) }, "\u2193") : null,
    !field.is_core ? el("button", { class: "btn btn--danger btn--sm", type: "button", onclick: () => deleteField(field) }, "حذف") : null
  );

  return el("div", { class: "fieldrow" },
    el("div", { class: "fieldrow__main" },
      el("div", { class: "fieldrow__label" }, field.label,
        field.required ? el("span", { class: "pill pill--neutral", text: "مطلوب", style: "margin-inline-start:0.5rem" }) : null,
        field.locked ? el("span", { class: "pill pill--neutral", text: "حقل دخول", style: "margin-inline-start:0.35rem" }) : null,
        !field.enabled ? el("span", { class: "pill pill--rejected", text: "مخفي", style: "margin-inline-start:0.35rem" }) : null
      ),
      el("div", { class: "fieldrow__key", text: `${field.key} - ${TYPE_LABEL[field.type] || field.type}` })
    ),
    controls
  );
}

async function move(index, delta) {
  const keys = formDefinition.fields.map((f) => f.key);
  const target = index + delta;
  if (target < 0 || target >= keys.length) return;
  [keys[index], keys[target]] = [keys[target], keys[index]];
  try {
    await api(`${PREFIX}/form/reorder`, { method: "POST", body: { keys } });
    loadForm();
  } catch (err) { handleError(err); }
}

async function saveField(field) {
  try {
    await api(`${PREFIX}/form/fields/${encodeURIComponent(field.key)}`, {
      method: "PUT",
      body: {
        key: field.key, label: field.label, help_text: field.help_text || "",
        placeholder: field.placeholder || "", type: field.type,
        required: !!field.required, enabled: !!field.enabled,
        options: field.options || "", depends_on: field.depends_on || "",
        depends_val: field.depends_val || "", sort_order: field.sort_order || 0,
      },
    });
    toast("تم حفظ الحقل", "ok");
    loadForm();
  } catch (err) { handleError(err); }
}

async function deleteField(field) {
  if (!await confirmAction(`سيتم حذف الحقل "${field.label}" من الاستمارة.`, "حذف")) return;
  try {
    await api(`${PREFIX}/form/fields/${encodeURIComponent(field.key)}`, { method: "DELETE" });
    toast("تم حذف الحقل", "ok");
    loadForm();
  } catch (err) { handleError(err); }
}

$("#addFieldBtn").addEventListener("click", () => editField(null));

function editField(field) {
  const isNew = !field;
  const model = field || { key: "", label: "", help_text: "", placeholder: "", type: "text", required: false, enabled: true, options: "" };

  const body = el("div", {},
    el("div", { class: "notice notice--error", id: "fieldNotice", hidden: true }),
    isNew ? el("div", { class: "field" },
      el("label", { class: "label", for: "flKey", text: "معرف الحقل" }),
      el("input", { id: "flKey", type: "text", dir: "ltr", placeholder: "committee_name" }),
      el("p", { class: "help", text: "حروف إنجليزية وأرقام فقط، ويستخدم كعنوان عمود في ملف الإكسل" })
    ) : null,
    el("div", { class: "field" },
      el("label", { class: "label", for: "flLabel", text: "العنوان الظاهر" }),
      el("input", { id: "flLabel", type: "text", value: model.label })
    ),
    el("div", { class: "field" },
      el("label", { class: "label", for: "flHelp", text: "نص مساعد" }),
      el("input", { id: "flHelp", type: "text", value: model.help_text || "" })
    ),
    el("div", { class: "field" },
      el("label", { class: "label", for: "flPlaceholder", text: "نص توضيحي داخل الحقل" }),
      el("input", { id: "flPlaceholder", type: "text", value: model.placeholder || "" })
    ),
    el("div", { class: "field" },
      el("label", { class: "label", for: "flType", text: "نوع الحقل" }),
      el("select", { id: "flType", disabled: (!isNew && model.is_core) || undefined },
        Object.entries(TYPE_LABEL).map(([value, label]) =>
          el("option", { value, text: label, selected: model.type === value || undefined })
        )
      ),
      !isNew && model.is_core ? el("p", { class: "help", text: "لا يمكن تغيير نوع الحقول الأساسية" }) : null
    ),
    el("div", { class: "field", id: "optionsField", hidden: model.type !== "select" },
      el("label", { class: "label", for: "flOptions", text: "الخيارات" }),
      el("textarea", { id: "flOptions", rows: "4", placeholder: "خيار في كل سطر" }, model.options || "")
    ),
    el("div", { class: "checkline", style: "margin-bottom:1rem" },
      el("input", { type: "checkbox", id: "flRequired", checked: model.required || undefined, disabled: model.locked || undefined }),
      el("label", { for: "flRequired", text: "حقل مطلوب" })
    ),
    el("div", { class: "checkline" },
      el("input", { type: "checkbox", id: "flEnabled", checked: model.enabled !== false || undefined, disabled: model.locked || undefined }),
      el("label", { for: "flEnabled", text: "ظاهر في الاستمارة" })
    )
  );

  const dialog = el("dialog", {},
    el("div", { class: "dialog__head", text: isNew ? "إضافة حقل جديد" : "تحرير الحقل" }),
    el("div", { class: "dialog__body" }, body),
    el("div", { class: "dialog__foot" },
      el("button", { class: "btn btn--ghost", type: "button", onclick: () => { dialog.close(); dialog.remove(); } }, "إلغاء"),
      el("button", { class: "btn", type: "button", id: "flSave" }, "حفظ")
    )
  );
  document.body.append(dialog);
  dialog.showModal();

  dialog.querySelector("#flType").addEventListener("change", (e) => {
    dialog.querySelector("#optionsField").hidden = e.target.value !== "select";
  });

  dialog.querySelector("#flSave").addEventListener("click", async () => {
    const notice = dialog.querySelector("#fieldNotice");
    notice.hidden = true;
    const payload = {
      key: isNew ? dialog.querySelector("#flKey").value : model.key,
      label: dialog.querySelector("#flLabel").value,
      help_text: dialog.querySelector("#flHelp").value,
      placeholder: dialog.querySelector("#flPlaceholder").value,
      type: dialog.querySelector("#flType").value,
      options: dialog.querySelector("#flOptions").value,
      required: dialog.querySelector("#flRequired").checked,
      enabled: dialog.querySelector("#flEnabled").checked,
      depends_on: model.depends_on || "",
      depends_val: model.depends_val || "",
      sort_order: model.sort_order || 0,
    };
    try {
      if (isNew) {
        await api(`${PREFIX}/form/fields`, { method: "POST", body: payload });
      } else {
        await api(`${PREFIX}/form/fields/${encodeURIComponent(model.key)}`, { method: "PUT", body: payload });
      }
      dialog.close();
      dialog.remove();
      toast("تم حفظ الحقل", "ok");
      loadForm();
    } catch (err) {
      notice.textContent = err.message;
      notice.hidden = false;
    }
  });
}

$("#formSettings").addEventListener("submit", async (event) => {
  event.preventDefault();
  const btn = $("#fsSave");
  busy(btn, true, "جارٍ الحفظ");
  try {
    const res = await api(`${PREFIX}/form/settings`, {
      method: "PUT",
      body: {
        title: $("#fsTitle").value, intro: $("#fsIntro").value, terms: $("#fsTerms").value,
        agree_text: $("#fsAgree").value, success_message: $("#fsSuccess").value,
      },
    });
    toast(res.message, "ok");
  } catch (err) { handleError(err); } finally { busy(btn, false); }
});

/* ------------------------------------------------------------ registration */

document.querySelectorAll("[data-reg]").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const action = btn.dataset.reg;
    if (action === "close" &&
        !await confirmAction("سيتوقف استقبال طلبات العضوية الجديدة فوراً.", "إغلاق التسجيل")) return;
    clearFieldErrors($("[data-panel=settings]"));
    try {
      const res = await api(`${PREFIX}/registration`, {
        method: "PUT",
        body: { action, close_at: $("#closeAt").value, closed_message: $("#closedMsg").value },
      });
      toast(res.message, "ok");
      loadOverview();
    } catch (err) { handleError(err, $("[data-panel=settings]")); }
  });
});

/* ------------------------------------------------------------ export */

function download() {
  // A plain navigation keeps the browser's own download UI and the filename.
  window.location.href = `${PREFIX}/export`;
}
$("#exportBtn").addEventListener("click", download);
$("#exportBtn2").addEventListener("click", download);

/* ------------------------------------------------------------ prompt */

function promptText(label, title) {
  return new Promise((resolve) => {
    const input = el("textarea", { rows: "3" });
    const dialog = el("dialog", {},
      el("div", { class: "dialog__head", text: title }),
      el("div", { class: "dialog__body" },
        el("label", { class: "label", text: label }), input),
      el("div", { class: "dialog__foot" },
        el("button", { class: "btn btn--ghost", type: "button", onclick: () => finish(null) }, "إلغاء"),
        el("button", { class: "btn btn--danger", type: "button", onclick: () => finish(input.value) }, "تأكيد")
      )
    );
    function finish(value) { dialog.close(); dialog.remove(); resolve(value); }
    dialog.addEventListener("cancel", (e) => { e.preventDefault(); finish(null); });
    document.body.append(dialog);
    dialog.showModal();
  });
}
