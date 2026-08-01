/* Shared helpers for every page. */

export const AR_MONTHS = ["يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو",
  "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"];

/* ------------------------------------------------------------ network */

export class ApiError extends Error {
  constructor(message, field, status) {
    super(message);
    this.field = field;
    this.status = status;
  }
}

export async function api(path, options = {}) {
  const opts = {
    credentials: "same-origin",
    headers: { "X-Requested-With": "matam" },
    ...options,
  };
  if (opts.body !== undefined && !(opts.body instanceof FormData)) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(opts.body);
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch {
    throw new ApiError("تعذر الاتصال بالخادم. تحقق من الاتصال بالإنترنت", "", 0);
  }
  if (res.status === 204) return null;

  const type = res.headers.get("content-type") || "";
  if (!type.includes("application/json")) {
    if (!res.ok) throw new ApiError("حدث خطأ غير متوقع", "", res.status);
    return res;
  }
  const data = await res.json();
  if (!res.ok) throw new ApiError(data.error || "حدث خطأ غير متوقع", data.field || "", res.status);
  return data;
}

/* ------------------------------------------------------------ dom */

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
    else if (v === true) node.setAttribute(k, "");
    else node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

/* ------------------------------------------------------------ toasts */

function toastStack() {
  let stack = $(".toast-stack");
  if (!stack) {
    stack = el("div", { class: "toast-stack", role: "status", "aria-live": "polite" });
    document.body.append(stack);
  }
  return stack;
}

export function toast(message, kind = "") {
  const node = el("div", { class: "toast" + (kind ? " toast--" + kind : ""), text: message });
  toastStack().append(node);
  setTimeout(() => node.remove(), 5000);
}

/* ------------------------------------------------------------ dates */

export function isoToDisplay(iso) {
  if (!iso || iso.length < 10) return iso || "";
  const [y, m, d] = iso.slice(0, 10).split("-");
  return `${d}-${m}-${y}`;
}

export function stampToDisplay(rfc) {
  if (!rfc) return "";
  const t = new Date(rfc);
  if (isNaN(t)) return "";
  return `${String(t.getDate()).padStart(2, "0")} ${AR_MONTHS[t.getMonth()]} ${t.getFullYear()}`;
}

/* ------------------------------------------------------------ numbers */

const ARABIC_INDIC = /[\u0660-\u0669\u06F0-\u06F9]/g;

export function normalizeDigits(value) {
  return String(value ?? "").replace(ARABIC_INDIC, (c) => {
    const code = c.charCodeAt(0);
    const base = code >= 0x06f0 ? 0x06f0 : 0x0660;
    return String(code - base);
  });
}

export function digitsOnly(value) {
  return normalizeDigits(value).replace(/\D/g, "");
}

/* Restricts an input to digits, converting Arabic-Indic numerals as they type. */
export function bindNumeric(input, maxLength) {
  input.setAttribute("inputmode", "numeric");
  input.setAttribute("dir", "ltr");
  input.addEventListener("input", () => {
    const cleaned = digitsOnly(input.value).slice(0, maxLength);
    if (cleaned !== input.value) input.value = cleaned;
  });
}

/* ------------------------------------------------------------ errors */

export function clearFieldErrors(root = document) {
  $$(".field.has-error", root).forEach((f) => f.classList.remove("has-error"));
  $$(".field-error", root).forEach((e) => (e.textContent = ""));
}

export function showFieldError(key, message, root = document) {
  const holder = root.querySelector(`[data-field="${CSS.escape(key)}"]`);
  if (!holder) return false;
  holder.classList.add("has-error");
  const slot = holder.querySelector(".field-error");
  if (slot) slot.textContent = message;
  holder.scrollIntoView({ block: "center", behavior: "smooth" });
  const input = holder.querySelector("input, select, textarea");
  if (input) input.focus({ preventScroll: true });
  return true;
}

export function handleError(err, root = document) {
  if (err instanceof ApiError && err.field && showFieldError(err.field, err.message, root)) return;
  toast(err.message || "حدث خطأ غير متوقع", "error");
}

/* ------------------------------------------------------------ buttons */

export function busy(button, on, labelWhenBusy) {
  if (on) {
    button.dataset.label = button.textContent;
    button.disabled = true;
    clear(button);
    button.append(el("span", { class: "spinner", "aria-hidden": "true" }), labelWhenBusy || "جارٍ التنفيذ");
  } else {
    button.disabled = false;
    button.textContent = button.dataset.label || button.textContent;
  }
}

/* ------------------------------------------------------------ chrome */

export async function paintChrome() {
  let cfg = {};
  try {
    cfg = await api("/api/public/config");
  } catch {
    return cfg;
  }
  $$("[data-org-name]").forEach((n) => (n.textContent = cfg.org_name || ""));
  const foot = $("[data-footer]");
  if (foot) {
    clear(foot);
    foot.append(
      el("span", { class: "org", text: cfg.org_name || "" }),
      el("span", { text: "تصميم وتطوير: " + (cfg.footer_author || "") }),
      el("br"),
      cfg.footer_instagram
        ? el("a", {
            href: "https://instagram.com/" + cfg.footer_instagram.replace(/^@/, ""),
            rel: "noopener noreferrer",
            target: "_blank",
            text: cfg.footer_instagram,
          })
        : null
    );
  }
  const bar = $("[data-topbar]");
  if (bar) {
    if (cfg.registration_open && cfg.close_at_display) {
      clear(bar);
      bar.append("يغلق باب التسجيل في ", el("strong", { text: cfg.close_at_display }));
      bar.hidden = false;
    } else {
      bar.hidden = true;
    }
  }
  return cfg;
}

/* ------------------------------------------------------------ confirm */

export function confirmAction(message, confirmLabel = "تأكيد", danger = true) {
  return new Promise((resolve) => {
    const dialog = el("dialog", {},
      el("div", { class: "dialog__head", text: "تأكيد الإجراء" }),
      el("div", { class: "dialog__body" }, el("p", { text: message, style: "margin:0" })),
      el("div", { class: "dialog__foot" },
        el("button", { class: "btn btn--ghost", type: "button", onclick: () => finish(false) }, "إلغاء"),
        el("button", { class: "btn" + (danger ? " btn--danger" : ""), type: "button", onclick: () => finish(true) }, confirmLabel)
      )
    );
    function finish(value) {
      dialog.close();
      dialog.remove();
      resolve(value);
    }
    dialog.addEventListener("cancel", (e) => { e.preventDefault(); finish(false); });
    document.body.append(dialog);
    dialog.showModal();
  });
}
