import { $, api, busy, clear, confirmAction, el, handleError, paintChrome, toast } from "./app.js";

const content = $("#content");
let ballot = null;
let step = 0;                 // index into ballot.sections; equal to length means review
const picks = new Map();      // role_id -> Set of participant ids

paintChrome();

$("#logoutBtn").addEventListener("click", async () => {
  try { await api("/api/auth/logout", { method: "POST" }); } catch { /* leave anyway */ }
  window.location.replace("/");
});

function selectionsFor(roleId) {
  if (!picks.has(roleId)) picks.set(roleId, new Set());
  return picks.get(roleId);
}

function candidateCard(section, candidate) {
  const chosen = selectionsFor(section.role_id);
  const multi = section.max_choices > 1;
  const inputId = `c_${section.role_id}_${candidate.id}`;

  const input = el("input", {
    type: multi ? "checkbox" : "radio",
    name: `sec_${section.role_id}`,
    id: inputId,
    value: candidate.id,
    checked: chosen.has(candidate.id) || undefined,
  });

  input.addEventListener("change", () => {
    if (multi) {
      if (input.checked) {
        if (chosen.size >= section.max_choices) {
          input.checked = false;
          toast(`لا يمكن اختيار أكثر من ${section.max_choices} مرشحين في هذه الصفحة`, "error");
          return;
        }
        chosen.add(candidate.id);
      } else {
        chosen.delete(candidate.id);
      }
    } else {
      chosen.clear();
      chosen.add(candidate.id);
    }
    updateFooter();
  });

  const photo = candidate.photo
    ? el("img", { class: "candidate__photo", src: candidate.photo, alt: "", loading: "lazy" })
    : el("div", { class: "candidate__photo candidate__photo--none", "aria-hidden": "true" },
        (candidate.name || "؟").trim().charAt(0));

  return el("div", { class: "candidate" },
    input,
    el("label", { for: inputId },
      photo,
      el("div", { class: "candidate__body" },
        el("h3", { class: "candidate__name", text: candidate.name }),
        candidate.description ? el("p", { class: "candidate__desc", text: candidate.description }) : null
      )
    ),
    el("span", { class: "candidate__mark", "aria-hidden": "true" }, "\u2713")
  );
}

function updateFooter() {
  const next = $("#nextBtn");
  if (!next) return;
  if (step < ballot.sections.length) {
    next.disabled = selectionsFor(ballot.sections[step].role_id).size === 0;
  }
}

function renderSection() {
  const section = ballot.sections[step];
  const total = ballot.sections.length;
  const chosen = selectionsFor(section.role_id);

  clear(content);

  const rule = section.max_choices > 1
    ? `اختر حتى ${section.max_choices} من المرشحين`
    : "اختر مرشحاً واحداً";

  content.append(
    total > 1
      ? el("div", { class: "ballot-progress" },
          el("span", { text: `${step + 1} من ${total}` }),
          el("div", { class: "ballot-progress__track" },
            el("div", { class: "ballot-progress__fill", style: `width:${((step + 1) / (total + 1)) * 100}%` })
          )
        )
      : null,
    el("div", { class: "card" },
      el("h1", { text: section.name }),
      section.description ? el("p", { class: "lede", text: section.description }) : null,
      el("div", { class: "notice notice--info" },
        `${rule}. عدد الفائزين في هذه الصفحة: ${section.winners}.`),
      el("div", { class: "candidates" }, section.candidates.map((c) => candidateCard(section, c)))
    ),
    el("div", { class: "ballot-actions" },
      step > 0
        ? el("button", { class: "btn btn--ghost", type: "button", onclick: () => { step--; renderSection(); } }, "السابق")
        : el("span"),
      el("button", {
        class: "btn", type: "button", id: "nextBtn",
        disabled: chosen.size === 0 || undefined,
        onclick: () => { step++; step < ballot.sections.length ? renderSection() : renderReview(); },
      }, step === ballot.sections.length - 1 ? "مراجعة الاختيارات" : "التالي")
    )
  );
  window.scrollTo({ top: 0, behavior: "smooth" });
}

function renderReview() {
  clear(content);
  const list = el("ul", { class: "review-list" });
  for (const section of ballot.sections) {
    const chosen = selectionsFor(section.role_id);
    const names = section.candidates.filter((c) => chosen.has(c.id)).map((c) => c.name);
    list.append(el("li", {},
      el("span", { class: "role", text: section.name }),
      el("strong", { text: names.join("، ") || "لم يتم الاختيار" })
    ));
  }

  content.append(
    el("div", { class: "ballot-progress" },
      el("span", { text: "المراجعة الأخيرة" }),
      el("div", { class: "ballot-progress__track" }, el("div", { class: "ballot-progress__fill", style: "width:100%" }))
    ),
    el("div", { class: "card" },
      el("h1", { text: "مراجعة الاختيارات" }),
      el("p", { class: "lede", text: "تأكد من اختياراتك قبل الإرسال. لا يمكن تعديل التصويت بعد إرساله." }),
      list
    ),
    el("div", { class: "ballot-actions" },
      el("button", { class: "btn btn--ghost", type: "button", onclick: () => { step = ballot.sections.length - 1; renderSection(); } }, "تعديل"),
      el("button", { class: "btn", type: "button", id: "sendBtn", onclick: send }, "إرسال التصويت")
    )
  );
  window.scrollTo({ top: 0, behavior: "smooth" });
}

async function send() {
  const confirmed = await confirmAction(
    "سيتم إرسال تصويتك نهائياً ولا يمكن تعديله بعد ذلك. هل تريد المتابعة؟",
    "إرسال التصويت", false
  );
  if (!confirmed) return;

  const btn = $("#sendBtn");
  busy(btn, true, "جارٍ الإرسال");
  const selections = {};
  for (const section of ballot.sections) {
    selections[String(section.role_id)] = Array.from(selectionsFor(section.role_id));
  }
  try {
    await api("/api/member/ballot", { method: "POST", body: { selections } });
    renderDone();
  } catch (err) {
    busy(btn, false);
    handleError(err);
  }
}

function renderDone() {
  clear(content);
  content.append(
    el("div", { class: "center-screen" },
      el("span", { class: "mark", "aria-hidden": "true" }, "\u2713"),
      el("h1", { text: "تم تسجيل تصويتك بنجاح" }),
      el("p", { text: "شكراً لمشاركتك في انتخابات الجمعية العمومية." }),
      el("a", { class: "btn btn--ghost", href: "/member" }, "العودة إلى الصفحة الرئيسية")
    )
  );
}

async function load() {
  try {
    ballot = await api("/api/member/ballot");
  } catch (err) {
    if (err.status === 401) { window.location.replace("/"); return; }
    clear(content);
    content.append(el("div", { class: "card" },
      el("p", { class: "empty", text: err.message }),
      el("div", { class: "btn-row btn-row--end" }, el("a", { class: "btn btn--ghost", href: "/member" }, "رجوع"))
    ));
    return;
  }
  if (ballot.has_voted) { renderDone(); return; }
  if (!ballot.sections || ballot.sections.length === 0) {
    clear(content);
    content.append(el("div", { class: "card" }, el("p", { class: "empty", text: "لا يوجد مرشحون في هذا التصويت بعد." })));
    return;
  }
  document.title = ballot.title || document.title;
  renderSection();
}

load();
