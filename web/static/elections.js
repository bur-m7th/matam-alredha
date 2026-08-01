import {
  $, $$, api, busy, clear, clearFieldErrors, confirmAction, el, handleError, paintChrome, toast,
} from "./app.js";
import { emptyState, setupLoginGate, setupTabs, stat } from "./dashboard.js";

// The dashboard lives at a segment the operator chooses, and its API sits
// directly beneath it. Deriving the base from the current URL means the secret
// segment never has to be compiled in.
const PREFIX = location.pathname.replace(/\/+$/, "") + "/api";
let state = null;

paintChrome();
setupTabs(() => load());
setupLoginGate({ prefix: PREFIX, onReady: () => load() });

const STATUS_TEXT = {
  draft: ["notice notice--warn", "التصويت في وضع الإعداد. لا يظهر للأعضاء بعد."],
  open: ["notice notice--ok", "التصويت مفتوح ومتاح للأعضاء المعتمدين."],
  closed: ["notice notice--info", "التصويت مغلق. النتائج مثبتة."],
};

async function load() {
  try { state = await api(`${PREFIX}/overview`); } catch (err) { handleError(err); return; }

  clear($("#stats")).append(
    stat(state.voted, "عضو صوّت"),
    stat(state.eligible, "عضو له حق التصويت"),
    stat(state.eligible ? Math.round((state.voted / state.eligible) * 100) + "%" : "0%", "نسبة المشاركة"),
    stat(state.mode === "single" ? "قائمة واحدة" : "حسب المناصب", "نظام التصويت")
  );

  const [cls, text] = STATUS_TEXT[state.status] || STATUS_TEXT.draft;
  const notice = $("#statusNotice");
  notice.className = cls;
  notice.textContent = text;

  $("#modeRoles").checked = state.mode === "roles";
  $("#modeSingle").checked = state.mode === "single";
  $("#vTitle").value = state.title || "";
  $("#vAnnounce").value = state.announcement || "";
  $("#vClosed").value = state.closed_message || "";
  $("#vWinners").value = state.single_winners;
  $("#vSelections").value = state.single_selections;
  syncModeUI();

  renderRoles();
  renderCandidates();
  renderResults();
}

function syncModeUI() {
  const single = $("#modeSingle").checked;
  $("#singleSettings").hidden = !single;
  $("#rolesCard").hidden = single;
}
$$('input[name="mode"]').forEach((r) => r.addEventListener("change", syncModeUI));

/* ------------------------------------------------------------ settings */

$("#settingsForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  clearFieldErrors($("#settingsForm"));
  const btn = $("#settingsSave");
  busy(btn, true, "جارٍ الحفظ");
  try {
    const res = await api(`${PREFIX}/settings`, {
      method: "PUT",
      body: {
        mode: $("#modeSingle").checked ? "single" : "roles",
        title: $("#vTitle").value,
        announcement: $("#vAnnounce").value,
        closed_message: $("#vClosed").value,
        single_winners: Number($("#vWinners").value) || 1,
        single_selections: Number($("#vSelections").value) || 1,
      },
    });
    toast(res.message, "ok");
    load();
  } catch (err) { handleError(err, $("#settingsForm")); } finally { busy(btn, false); }
});

$$("[data-status]").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const status = btn.dataset.status;
    const prompts = {
      open: "سيصبح التصويت متاحاً لجميع الأعضاء المعتمدين فوراً.",
      closed: "سيتوقف استقبال الأصوات نهائياً.",
      draft: "سيختفي التصويت من صفحات الأعضاء.",
    };
    if (!await confirmAction(prompts[status], "متابعة", status !== "open")) return;
    try {
      const res = await api(`${PREFIX}/status`, { method: "POST", body: { status } });
      toast(res.message, "ok");
      load();
    } catch (err) { handleError(err); }
  });
});

/* ------------------------------------------------------------ roles */

function renderRoles() {
  const list = clear($("#rolesList"));
  if (!state.roles || state.roles.length === 0) {
    list.append(el("p", { class: "empty", text: "لم تتم إضافة أي منصب بعد" }));
    return;
  }
  for (const role of state.roles) {
    list.append(el("div", { class: "fieldrow" },
      el("div", { class: "fieldrow__main" },
        el("div", { class: "fieldrow__label", text: role.name }),
        el("div", { class: "fieldrow__key" },
          `عدد الفائزين: ${role.winners_count} - عدد الاختيارات المسموحة: ${role.selections_allowed}`)
      ),
      el("div", { class: "fieldrow__ctrl" },
        el("button", { class: "btn btn--ghost btn--sm", type: "button", onclick: () => editRole(role) }, "تحرير"),
        el("button", { class: "btn btn--danger btn--sm", type: "button", onclick: () => removeRole(role) }, "حذف")
      )
    ));
  }
}

$("#addRoleBtn").addEventListener("click", () => editRole(null));

function editRole(role) {
  const isNew = !role;
  const model = role || { name: "", description: "", winners_count: 1, selections_allowed: 1, sort_order: 0 };
  const dialog = buildDialog(isNew ? "إضافة منصب" : "تحرير المنصب",
    el("div", {},
      el("div", { class: "notice notice--error", id: "roleNotice", hidden: true }),
      el("div", { class: "field" },
        el("label", { class: "label", for: "rName", text: "اسم المنصب" }),
        el("input", { id: "rName", type: "text", value: model.name })
      ),
      el("div", { class: "field" },
        el("label", { class: "label", for: "rDesc", text: "وصف مختصر" }),
        el("textarea", { id: "rDesc", rows: "2" }, model.description || "")
      ),
      el("div", { class: "grid-2" },
        el("div", { class: "field" },
          el("label", { class: "label", for: "rWinners", text: "عدد الفائزين" }),
          el("input", { id: "rWinners", type: "number", min: "1", value: model.winners_count })
        ),
        el("div", { class: "field" },
          el("label", { class: "label", for: "rSelections", text: "عدد الاختيارات المسموحة" }),
          el("input", { id: "rSelections", type: "number", min: "1", value: model.selections_allowed })
        )
      )
    ));

  dialog.querySelector("#dlgSave").addEventListener("click", async () => {
    const notice = dialog.querySelector("#roleNotice");
    notice.hidden = true;
    const payload = {
      name: dialog.querySelector("#rName").value,
      description: dialog.querySelector("#rDesc").value,
      winners_count: Number(dialog.querySelector("#rWinners").value) || 1,
      selections_allowed: Number(dialog.querySelector("#rSelections").value) || 1,
      sort_order: model.sort_order || 0,
    };
    try {
      if (isNew) await api(`${PREFIX}/roles`, { method: "POST", body: payload });
      else await api(`${PREFIX}/roles/${role.id}`, { method: "PUT", body: payload });
      dialog.close(); dialog.remove();
      toast("تم حفظ المنصب", "ok");
      load();
    } catch (err) {
      notice.textContent = err.message;
      notice.hidden = false;
    }
  });
}

async function removeRole(role) {
  if (!await confirmAction(`سيتم حذف المنصب "${role.name}" وجميع مرشحيه.`, "حذف")) return;
  try {
    const res = await api(`${PREFIX}/roles/${role.id}`, { method: "DELETE" });
    toast(res.message, "ok");
    load();
  } catch (err) { handleError(err); }
}

/* ------------------------------------------------------------ candidates */

function renderCandidates() {
  const list = clear($("#candidatesList"));
  const sections = state.sections || [];
  const total = sections.reduce((n, s) => n + s.candidates.length, 0);
  if (total === 0) {
    list.append(emptyState("لم تتم إضافة أي مرشح بعد"));
    return;
  }
  for (const section of sections) {
    if (section.candidates.length === 0) continue;
    list.append(
      el("div", { class: "section-title" }, el("h2", { text: section.name })),
      el("div", { class: "candidates" }, section.candidates.map(candidateCard))
    );
  }
}

function candidateCard(candidate) {
  const photo = candidate.photo
    ? el("img", { class: "candidate__photo", src: candidate.photo, alt: "", loading: "lazy" })
    : el("div", { class: "candidate__photo candidate__photo--none", "aria-hidden": "true" },
        (candidate.name || "؟").trim().charAt(0));

  return el("div", { class: "candidate" },
    photo,
    el("div", { class: "candidate__body" },
      el("h3", { class: "candidate__name", text: candidate.name }),
      candidate.description ? el("p", { class: "candidate__desc", text: candidate.description }) : null,
      el("div", { class: "btn-row", style: "margin-top:0.75rem" },
        el("button", { class: "btn btn--ghost btn--sm", type: "button", onclick: () => editCandidate(candidate) }, "تحرير"),
        el("button", { class: "btn btn--danger btn--sm", type: "button", onclick: () => removeCandidate(candidate) }, "حذف")
      )
    )
  );
}

$("#addCandidateBtn").addEventListener("click", () => editCandidate(null));

function editCandidate(candidate) {
  const isNew = !candidate;
  const model = candidate || { name: "", description: "", photo: "", role_id: 0, sort_order: 0 };
  let photoPath = model.photo || "";

  const preview = el("img", {
    src: photoPath || "",
    alt: "",
    style: "width:8rem;aspect-ratio:4/5;object-fit:cover;border-radius:8px;border:1px solid var(--sand);background:var(--stone-deep)",
    hidden: !photoPath,
  });

  const roleSelect = el("select", { id: "cRole" },
    el("option", { value: "0", text: state.mode === "single" ? "ضمن القائمة الواحدة" : "بدون منصب" }),
    (state.roles || []).map((r) =>
      el("option", { value: String(r.id), text: r.name, selected: String(model.role_id) === String(r.id) || undefined })
    )
  );

  const dialog = buildDialog(isNew ? "إضافة مرشح" : "تحرير بيانات المرشح",
    el("div", {},
      el("div", { class: "notice notice--error", id: "candNotice", hidden: true }),
      el("div", { class: "field" },
        el("label", { class: "label", for: "cName", text: "اسم المرشح" }),
        el("input", { id: "cName", type: "text", value: model.name })
      ),
      el("div", { class: "field" },
        el("label", { class: "label", for: "cDesc", text: "نبذة تعريفية" }),
        el("textarea", { id: "cDesc", rows: "4" }, model.description || "")
      ),
      state.mode === "single" ? null : el("div", { class: "field" },
        el("label", { class: "label", for: "cRole", text: "المنصب" }),
        roleSelect
      ),
      el("div", { class: "field" },
        el("span", { class: "label", text: "الصورة" }),
        el("div", { style: "display:flex;gap:1rem;align-items:flex-start;flex-wrap:wrap" },
          preview,
          el("div", { style: "flex:1 1 12rem" },
            el("input", { type: "file", id: "cPhoto", accept: "image/jpeg,image/png,image/webp" }),
            el("p", { class: "help", text: "بصيغة JPG أو PNG أو WEBP، وبحد أقصى خمسة ميغابايت" }),
            photoPath
              ? el("button", {
                  class: "btn btn--ghost btn--sm", type: "button",
                  onclick: (e) => { photoPath = ""; preview.hidden = true; e.currentTarget.remove(); },
                }, "إزالة الصورة")
              : null
          )
        )
      )
    ));

  dialog.querySelector("#cPhoto").addEventListener("change", async (event) => {
    const file = event.target.files[0];
    if (!file) return;
    const data = new FormData();
    data.append("photo", file);
    try {
      const res = await api(`${PREFIX}/photo`, { method: "POST", body: data });
      photoPath = res.photo;
      preview.src = photoPath;
      preview.hidden = false;
    } catch (err) {
      handleError(err);
      event.target.value = "";
    }
  });

  dialog.querySelector("#dlgSave").addEventListener("click", async () => {
    const notice = dialog.querySelector("#candNotice");
    notice.hidden = true;
    const payload = {
      name: dialog.querySelector("#cName").value,
      description: dialog.querySelector("#cDesc").value,
      photo: photoPath,
      role_id: state.mode === "single" ? 0 : Number(roleSelect.value) || 0,
      sort_order: model.sort_order || 0,
    };
    try {
      if (isNew) await api(`${PREFIX}/participants`, { method: "POST", body: payload });
      else await api(`${PREFIX}/participants/${candidate.id}`, { method: "PUT", body: payload });
      dialog.close(); dialog.remove();
      toast("تم حفظ بيانات المرشح", "ok");
      load();
    } catch (err) {
      notice.textContent = err.message;
      notice.hidden = false;
    }
  });
}

async function removeCandidate(candidate) {
  if (!await confirmAction(`سيتم حذف المرشح "${candidate.name}".`, "حذف")) return;
  try {
    const res = await api(`${PREFIX}/participants/${candidate.id}`, { method: "DELETE" });
    toast(res.message, "ok");
    load();
  } catch (err) { handleError(err); }
}

/* ------------------------------------------------------------ results */

function renderResults() {
  const list = clear($("#resultsList"));
  const sections = state.sections || [];
  if (sections.length === 0) {
    list.append(emptyState("لا توجد نتائج بعد"));
    return;
  }
  for (const section of sections) {
    // Ranking is by votes; the top N by the configured winner count are marked.
    const ranked = [...section.candidates].sort((a, b) => (b.votes || 0) - (a.votes || 0));
    const winners = new Set(ranked.slice(0, section.winners).filter((c) => (c.votes || 0) > 0).map((c) => c.id));

    const rows = ranked.map((c) =>
      el("div", { class: "result-row" + (winners.has(c.id) ? " is-winner" : "") },
        el("div", { class: "result-head" },
          el("span", { class: "result-name", text: c.name }),
          el("span", { class: "result-num", text: `${c.votes || 0} صوت - ${c.share || "0.0"}%` })
        ),
        el("div", { class: "result-track" },
          el("div", { class: "result-fill", style: `width:${Math.min(100, Number(c.share) || 0)}%` }))
      )
    );

    list.append(el("div", { class: "card" },
      el("h2", { text: section.name }),
      el("p", { class: "help", text: `مجموع الأصوات: ${section.total_votes} - عدد الفائزين: ${section.winners}` }),
      el("div", { style: "margin-top:0.75rem" }, rows.length ? rows : el("p", { class: "empty", text: "لا يوجد مرشحون" }))
    ));
  }
}

$("#resetBtn").addEventListener("click", async () => {
  if (!await confirmAction("سيتم مسح جميع الأصوات نهائياً ولا يمكن التراجع.", "مسح الأصوات")) return;
  try {
    const res = await api(`${PREFIX}/reset`, { method: "POST" });
    toast(res.message, "ok");
    load();
  } catch (err) { handleError(err); }
});

/* ------------------------------------------------------------ dialog */

function buildDialog(title, body) {
  const dialog = el("dialog", {},
    el("div", { class: "dialog__head", text: title }),
    el("div", { class: "dialog__body" }, body),
    el("div", { class: "dialog__foot" },
      el("button", { class: "btn btn--ghost", type: "button", onclick: () => { dialog.close(); dialog.remove(); } }, "إلغاء"),
      el("button", { class: "btn", type: "button", id: "dlgSave" }, "حفظ")
    )
  );
  document.body.append(dialog);
  dialog.showModal();
  return dialog;
}
