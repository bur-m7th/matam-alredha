/* Renders the admin-defined form definition into real inputs.
   Used by the public registration page and by the member editor in the
   membership dashboard, so both always match the current definition. */

import { bindNumeric, el } from "./app.js";

const NUMERIC = { cpr: 9, phone: 8 };

function control(field, value) {
  const id = "f_" + field.key;

  if (field.type === "yesno") {
    const group = el("div", { class: "choice", role: "radiogroup", "aria-labelledby": id + "_label" });
    for (const [val, label] of [["yes", "نعم"], ["no", "لا"]]) {
      const inputId = `${id}_${val}`;
      group.append(
        el("input", {
          type: "radio", id: inputId, name: field.key, value: val,
          checked: value === val || undefined,
        }),
        el("label", { for: inputId, text: label })
      );
    }
    return group;
  }

  if (field.type === "select") {
    const select = el("select", { id, name: field.key });
    select.append(el("option", { value: "", text: "اختر" }));
    for (const opt of (field.options || "").split("\n").map((o) => o.trim()).filter(Boolean)) {
      select.append(el("option", { value: opt, text: opt, selected: value === opt || undefined }));
    }
    return select;
  }

  if (field.type === "textarea") {
    return el("textarea", { id, name: field.key, placeholder: field.placeholder || "" }, value || "");
  }

  const typeMap = {
    date: "date", email: "email", phone: "tel", number: "text", cpr: "text",
    arabic_name: "text", text: "text",
  };
  const input = el("input", {
    type: typeMap[field.type] || "text",
    id, name: field.key,
    value: value || "",
    placeholder: field.placeholder || "",
    autocomplete: field.key === "phone" ? "tel" : field.key === "email" ? "email" : "off",
  });

  if (field.type === "cpr" || field.type === "phone" || field.type === "number") {
    bindNumeric(input, NUMERIC[field.key] || 20);
  }
  if (field.type === "date") {
    input.max = new Date().toISOString().slice(0, 10);
    input.min = "1900-01-01";
  }
  if (field.type === "arabic_name") {
    input.setAttribute("lang", "ar");
    input.setAttribute("spellcheck", "false");
  }
  return input;
}

/* Conditional fields appear only once their controlling answer matches. */
function applyDependencies(root, fields) {
  for (const field of fields) {
    if (!field.depends_on) continue;
    const holder = root.querySelector(`[data-field="${CSS.escape(field.key)}"]`);
    if (!holder) continue;
    const watched = root.querySelectorAll(`[name="${CSS.escape(field.depends_on)}"]`);
    const sync = () => {
      let current = "";
      for (const node of watched) {
        if (node.type === "radio" || node.type === "checkbox") {
          if (node.checked) current = node.value;
        } else {
          current = node.value;
        }
      }
      const show = current === field.depends_val;
      holder.hidden = !show;
      if (!show) {
        holder.classList.remove("has-error");
        const slot = holder.querySelector(".field-error");
        if (slot) slot.textContent = "";
        holder.querySelectorAll("input, select, textarea").forEach((n) => {
          if (n.type === "radio" || n.type === "checkbox") n.checked = false;
          else n.value = "";
        });
      }
    };
    watched.forEach((node) => node.addEventListener("change", sync));
    sync();
  }
}

/* Groups the three address parts onto one row on wider screens. */
const ADDRESS_KEYS = new Set(["house", "road", "block"]);

export function renderFields(container, fields, values = {}) {
  container.replaceChildren();
  let addressRow = null;

  for (const field of fields) {
    const isAddress = ADDRESS_KEYS.has(field.key) && !field.depends_on;
    const labelId = "f_" + field.key + "_label";

    const holder = el("div", { class: "field", "data-field": field.key },
      el("span", { class: "label", id: labelId },
        field.label,
        field.required ? el("span", { class: "req", text: "*", "aria-label": "مطلوب" }) : null
      ),
      control(field, values[field.key]),
      field.help_text ? el("p", { class: "help", text: field.help_text }) : null,
      el("p", { class: "field-error", role: "alert" })
    );

    // Point the visible label at its control where a single control exists.
    const single = holder.querySelector("input:not([type=radio]), select, textarea");
    if (single) {
      const span = holder.querySelector(".label");
      const label = el("label", { for: single.id, class: "label", id: labelId });
      label.append(...span.childNodes);
      span.replaceWith(label);
    }

    if (isAddress) {
      if (!addressRow) {
        addressRow = el("div", { class: "grid-3" });
        container.append(addressRow);
      }
      addressRow.append(holder);
    } else {
      addressRow = null;
      container.append(holder);
    }
  }

  applyDependencies(container, fields);
}

export function collectValues(container, fields) {
  const values = {};
  for (const field of fields) {
    const holder = container.querySelector(`[data-field="${CSS.escape(field.key)}"]`);
    if (!holder || holder.hidden) { values[field.key] = ""; continue; }
    const nodes = holder.querySelectorAll(`[name="${CSS.escape(field.key)}"]`);
    let value = "";
    for (const node of nodes) {
      if (node.type === "radio" || node.type === "checkbox") {
        if (node.checked) value = node.value;
      } else {
        value = node.value;
      }
    }
    values[field.key] = value.trim();
  }
  return values;
}
