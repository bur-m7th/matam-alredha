import { $, api, clear, el, handleError, paintChrome } from "./app.js";

const content = $("#content");
paintChrome();

$("#logoutBtn").addEventListener("click", async () => {
  try { await api("/api/auth/logout", { method: "POST" }); } catch { /* leave anyway */ }
  window.location.replace("/");
});

function card(...children) {
  return el("div", { class: "card" }, ...children);
}

function render(state) {
  $("#memberName").textContent = state.name || "";
  clear(content);

  // A withheld membership takes precedence over every other state.
  if (state.suspended) {
    content.append(card(
      el("h2", { text: "عضويتك موقوفة حالياً" }),
      el("p", { class: "lede", text: state.message || "يرجى مراجعة المأتم", style: "white-space:pre-wrap" }),
      el("p", { class: "help", text: "لا يمكن المشاركة في التصويت أو الخدمات حتى تُراجع الإدارة حالتك." })
    ));
    return;
  }

  // Standing for election is offered above whatever else is going on, since the
  // window is short and easy to miss.
  const standing = candidacyCard(state);

  if (state.has_voted) {
    if (standing) content.append(standing);
    content.append(card(
      el("h2", { text: "تم تسجيل تصويتك" }),
      el("p", { class: "lede", text: "شكراً لمشاركتك. لا يمكن تعديل التصويت بعد إرساله." }),
      el("p", { class: "help", text: "ستُعلن النتائج من قبل لجنة الانتخابات." })
    ));
    return;
  }

  if (state.voting_open) {
    if (standing) content.append(standing);
    content.append(card(
      el("h2", { text: state.voting_title || "التصويت متاح الآن" }),
      state.announcement ? el("p", { class: "lede", text: state.announcement, style: "white-space:pre-wrap" }) : null,
      el("p", { class: "help", text: "لك حق التصويت مرة واحدة فقط، ولا يمكن التعديل بعد الإرسال." }),
      el("a", { class: "btn btn--block", href: "/vote", style: "margin-top:1rem" }, "الانتقال إلى ورقة التصويت")
    ));
    return;
  }

  if (standing) {
    content.append(standing);
  }

  // Nothing announced yet: this is the state most members will see.
  content.append(
    el("div", { class: "center-screen", style: "padding-block:4rem" },
      el("span", { class: "mark", "aria-hidden": "true" }, "\u2014"),
      el("h1", { text: state.message || "لا يوجد شيء حتى الآن، بانتظار الاعلان من لجنة الانتخابات" }),
      state.announcement ? el("p", { text: state.announcement, style: "white-space:pre-wrap" }) : null
    )
  );
}

async function load() {
  try {
    render(await api("/api/member/me"));
  } catch (err) {
    if (err.status === 401) { window.location.replace("/"); return; }
    clear(content);
    content.append(card(el("p", { class: "empty", text: err.message })));
    handleError(err);
  }
}

load();
// Pick up an announcement without the member needing to refresh.
setInterval(load, 60000);


const CANDIDACY_LABEL = {
  submitted: ["طلب ترشحك قيد المراجعة", "notice notice--info"],
  accepted:  ["تم قبول ترشحك", "notice notice--ok"],
  returned:  ["أُعيد طلب ترشحك للتعديل", "notice notice--warn"],
};

// Returns the invitation or status card, or null when there is nothing to say.
function candidacyCard(state) {
  if (state.candidacy_status) {
    const [text, cls] = CANDIDACY_LABEL[state.candidacy_status] || CANDIDACY_LABEL.submitted;
    return card(
      el("h2", { text: "الترشح للانتخابات" }),
      el("div", { class: cls, text }),
      el("a", { class: "btn btn--ghost", href: "/candidacy", style: "margin-top:0.9rem" },
        state.candidacy_status === "returned" ? "تعديل الطلب" : "عرض الطلب")
    );
  }
  if (!state.candidacy_open) return null;
  if (!state.can_stand) {
    return card(
      el("h2", { text: "باب الترشح مفتوح" }),
      el("p", { class: "lede", text: state.stand_reason })
    );
  }
  return card(
    el("h2", { text: "باب الترشح مفتوح" }),
    el("p", { class: "lede", text: "يمكنك التقدم بطلب الترشح لعضوية مجلس الإدارة." }),
    el("a", { class: "btn btn--block", href: "/candidacy", style: "margin-top:0.9rem" },
      "تقديم طلب الترشح")
  );
}
