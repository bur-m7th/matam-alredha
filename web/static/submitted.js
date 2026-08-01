import { $, api, paintChrome } from "./app.js";

paintChrome();

// The wording of the confirmation is editable by the membership admin.
api("/api/public/form")
  .then((def) => {
    if (def.success_message) $("#successMessage").textContent = def.success_message;
  })
  .catch(() => {});
