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

  if (state.has_voted) {
    content.append(card(
      el("h2", { text: "تم تسجيل تصويتك" }),
      el("p", { class: "lede", text: "شكراً لمشاركتك. لا يمكن تعديل التصويت بعد إرساله." }),
      el("p", { class: "help", text: "ستُعلن النتائج من قبل لجنة الانتخابات." })
    ));
    return;
  }

  if (state.voting_open) {
    content.append(card(
      el("h2", { text: state.voting_title || "التصويت متاح الآن" }),
      state.announcement ? el("p", { class: "lede", text: state.announcement, style: "white-space:pre-wrap" }) : null,
      el("p", { class: "help", text: "لك حق التصويت مرة واحدة فقط، ولا يمكن التعديل بعد الإرسال." }),
      el("a", { class: "btn btn--block", href: "/vote", style: "margin-top:1rem" }, "الانتقال إلى ورقة التصويت")
    ));
    return;
  }

  // Nothing announced yet: this is the state most members will see.
  clear(content);
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
