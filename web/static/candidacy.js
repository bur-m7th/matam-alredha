import {
  $, api, busy, candidatePhoto, clear, el, handleError, paintChrome, skeletonLines, toast,
} from "./app.js";

const content = $("#content");
paintChrome();

// The page has its final shape before the request resolves.
content.append(el("div", { class: "card" }, skeletonLines(4)));

const STATUS = {
  submitted: { text: "طلبك قيد المراجعة من قبل لجنة الانتخابات", cls: "notice notice--info" },
  accepted:  { text: "تم قبول ترشحك. اسمك مدرج في ورقة التصويت", cls: "notice notice--ok" },
  returned:  { text: "أُعيد طلبك للتعديل. يرجى مراجعة الملاحظة أدناه وإعادة الإرسال", cls: "notice notice--warn" },
};

let view = null;
let pickedFile = null;

load();

async function load() {
  try {
    view = await api("/api/member/candidacy");
  } catch (err) {
    handleError(err);
    return;
  }
  render();
}

function render() {
  clear(content);

  if (!view.gate_open && !view.mine) {
    content.append(el("div", { class: "card" },
      el("h2", { text: "باب الترشح مغلق" }),
      el("p", { class: "lede", text: "سيُعلن عن فتح باب الترشح من قبل لجنة الانتخابات." })
    ));
    return;
  }

  if (!view.eligible) {
    content.append(el("div", { class: "card" },
      el("h2", { text: "لا يمكنك التقدم للترشح" }),
      el("p", { class: "lede", text: view.reason })
    ));
    return;
  }

  // Existing application, if any.
  if (view.mine) {
    const st = STATUS[view.mine.status] || STATUS.submitted;
    const card = el("div", { class: "card" },
      el("h2", { text: "طلب ترشحك" }),
      el("div", { class: st.cls, text: st.text }),
      view.mine.note
        ? el("div", { class: "notice notice--warn", style: "margin-top:0.75rem;white-space:pre-wrap" },
            el("strong", { text: "ملاحظة اللجنة: " }), view.mine.note)
        : null,
      el("div", { class: "cstatus", style: "margin-top:1rem" },
        el("div", { style: "width:7rem" }, candidatePhoto(view.mine)),
        el("div", {},
          el("div", { style: "font-weight:600" }, view.mine.name),
          el("div", { class: "help", text: "المنصب: " + (view.mine.role_name || "غير محدد") })
        )
      )
    );
    content.append(card);

    if (!view.can_amend) {
      content.append(el("div", { class: "card" },
        el("p", { class: "help", text: view.gate_open
          ? "لا يمكن تعديل الطلب بعد قبوله. للتغيير يرجى مراجعة لجنة الانتخابات."
          : "أُغلق باب الترشح، ولم يعد بالإمكان تعديل الطلب." })
      ));
      return;
    }
  }

  content.append(formCard());
}

function formCard() {
  const isEdit = Boolean(view.mine);

  // Each post carries its own minimum age. A post the member is too young for
  // stays visible, but disabled and labelled, so the requirement is plain
  // rather than surfacing as a rejection after they fill the form in.
  const elig = view.role_eligibility || {};
  const roleSelect = el("select", { id: "roleSel" },
    el("option", { value: "", text: "اختر المنصب" }),
    view.roles.map((r) => {
      const e = elig[String(r.id)] || { eligible: true };
      const min = r.min_age || view.min_age;
      return el("option", {
        value: String(r.id),
        text: e.eligible ? r.name : `${r.name} — يشترط ${min} سنة فأكثر`,
        disabled: e.eligible ? undefined : "disabled",
        selected: view.mine && String(view.mine.role_id) === String(r.id) ? "selected" : undefined,
      });
    })
  );

  const preview = el("div", { id: "preview", style: "width:9rem" },
    view.mine ? candidatePhoto(view.mine) : el("div", { class: "cphoto cphoto--none", "aria-hidden": "true" }, "؟")
  );

  const fileInput = el("input", {
    type: "file",
    id: "photoInput",
    accept: "image/jpeg,image/png,image/webp",
  });

  fileInput.addEventListener("change", (event) => {
    const file = event.target.files[0];
    if (!file) return;
    pickedFile = file;
    // Local preview: no round trip, so the member sees their choice at once.
    const url = URL.createObjectURL(file);
    clear(preview).append(el("img", {
      class: "cphoto", src: url, alt: "", width: "320", height: "320",
      onload: () => URL.revokeObjectURL(url),
    }));
  });

  const submit = el("button", { class: "btn btn--block", type: "button" },
    isEdit ? "إعادة إرسال الطلب" : "إرسال طلب الترشح");

  submit.addEventListener("click", async () => {
    if (!roleSelect.value) { toast("الرجاء اختيار المنصب", "error"); return; }
    if (!pickedFile && !isEdit) { toast("الرجاء إرفاق صورة شخصية", "error"); return; }

    const data = new FormData();
    data.append("role_id", roleSelect.value);
    if (pickedFile) data.append("photo", pickedFile);

    busy(submit, true, "جارٍ الإرسال");
    try {
      const res = await api("/api/member/candidacy", { method: "POST", body: data });
      toast(res.message, "ok");
      pickedFile = null;
      load();
    } catch (err) {
      handleError(err);
    } finally {
      busy(submit, false);
    }
  });

  return el("div", { class: "card" },
    el("h2", { text: isEdit ? "تعديل طلب الترشح" : "تقديم طلب الترشح" }),
    view.intro ? el("p", { class: "lede", text: view.intro, style: "white-space:pre-wrap" }) : null,

    // The name is not an input: it comes from the member's own record.
    el("div", { class: "field" },
      el("span", { class: "label", text: "الاسم" }),
      el("div", { class: "readonly-value", text: view.name }),
      el("p", { class: "help", text: "الاسم مأخوذ تلقائياً من بيانات عضويتك المسجّلة ولا يمكن تعديله من هنا." })
    ),

    el("div", { class: "field" },
      el("label", { class: "label", for: "roleSel", text: "المنصب المترشح له" }),
      roleSelect,
      el("p", { class: "help", text: "لكل منصب حد أدنى لعمر المترشح. المناصب غير المتاحة لك مبيَّن سببها." })
    ),

    el("div", { class: "field" },
      el("span", { class: "label", text: "الصورة الشخصية" }),
      el("div", { style: "display:flex;gap:1rem;align-items:flex-start;flex-wrap:wrap" },
        preview,
        el("div", { style: "flex:1 1 12rem" },
          fileInput,
          el("ul", { class: "help", style: "margin-top:0.5rem;padding-inline-start:1.1rem" },
            el("li", { text: "الصورة مربعة بنسبة 1:1" }),
            el("li", { text: `الحد الأدنى ${view.photo_min}×${view.photo_min} بكسل` }),
            el("li", { text: `الأكبر من ${view.photo_max}×${view.photo_max} يُصغّر تلقائياً` }),
            el("li", { text: `الحجم لا يتجاوز ${Math.round(view.photo_max_bytes / (1024 * 1024))} ميغابايت` })
          )
        )
      )
    ),
    submit
  );
}
