(() => {
  "use strict";
  const root = document.getElementById("page-builder");
  if (!root) return;
  const config = JSON.parse(root.dataset.state), labels = config.labels;
  let doc = config.draft.document,
    token = config.draft.updated_at || config.draft.base_updated_at,
    contentToken = config.draft.content_token;
  const list = document.getElementById("builder-sections"),
    status = document.getElementById("builder-status"),
    frame = document.getElementById("builder-preview");
  const csrf = root.querySelector('[name="_csrf"]').value,
    history = [],
    future = [];
  let publishing = false;
  let timer,
    previewTimer,
    pending = Promise.resolve(),
    dirty = false,
    blocked = false,
    serial = 0,
    previewSerial = 0;
  const copy = (value) => JSON.parse(JSON.stringify(value));
  const text = (key) => labels[key] || key;
  const notify = (key, error = false) => {
    status.textContent = text(key);
    status.setAttribute("role", error ? "alert" : "status");
  };
  const el = (tag, cls, value) => {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (value !== undefined) node.textContent = value;
    return node;
  };
  const buttons = () => {
    root.querySelector('[data-action="undo"]').disabled = !history.length;
    root.querySelector('[data-action="redo"]').disabled = !future.length;
    root.querySelector('[data-action="add"]').disabled =
      doc.sections.length >= 60;
    root.querySelector('[data-action="reset"]').disabled =
      doc.sections.length === 0 && !dirty;
    // Drafts save whatever the form state (the server checks only sizes);
    // publishing waits for every visible form to be complete.
    const invalid = formsUnpublishable();
    const publish = root.querySelector('[data-action="publish"]');
    const acknowledge = document.getElementById("builder-convert");
    if (publish) {
      publish.disabled = doc.sections.length === 0 || invalid ||
        (acknowledge && !acknowledge.checked);
    }
  };
  function remember() {
    history.push(copy(doc));
    if (history.length > 60) history.shift();
    future.length = 0;
  }
  function changed() {
    dirty = true;
    serial++;
    if (!blocked) notify("unsaved");
    buttons();
    clearTimeout(timer);
    clearTimeout(previewTimer);
    if (!blocked) timer = setTimeout(() => save().catch(() => {}), 900);
    previewTimer = setTimeout(preview, 250);
  }
  // focusKeys name controls (data-focus-key) to focus after the re-render,
  // the first enabled one winning; without one, focus returns to the section.
  function mutate(fn, ...focusKeys) {
    const focused = document.activeElement.closest("[data-section-id]")?.dataset
      .sectionId;
    remember();
    fn();
    render();
    const nodes = focusKeys.length ? [...list.querySelectorAll("[data-focus-key]")] : [];
    const keyed = focusKeys.map((key) => nodes.find((node) => node.dataset.focusKey === key && !node.disabled)).find(Boolean);
    if (keyed) keyed.focus();
    else if (focused) {
      const destination = [...list.children].find((node) =>
        node.dataset.sectionId === focused
      );
      (destination || list.firstElementChild)?.focus();
    }
    changed();
  }
  function button(key, callback, disabled = false) {
    const node = el(
      "button",
      "border border-line px-2 py-1 text-sm",
      text(key),
    );
    node.type = "button";
    node.disabled = disabled;
    node.addEventListener("click", callback);
    return node;
  }
  const imageTargets = new Map(), advancedSections = new Set();
 window.addEventListener("media:selected",event=>{const input=imageTargets.get(event.detail?.target);if(!input?.isConnected||typeof event.detail.url!=="string")return;input.value=event.detail.url;input.dispatchEvent(new Event("input",{bubbles:true}));});
 function field(target, key, parent, sectionID, entryIndex, labelKey = key) {
    const wrap = el("label", "block text-sm space-y-1", text(labelKey));
    const input = el(
      key === "text" ? "textarea" : "input",
      "w-full bg-panel border border-line p-2",
    );
    input.value = target[key] || "";
    input.maxLength = key === "link" || key === "image" || key === "secondary_link" || key === "action" ? 2048 : ["label", "value", "column", "search_label", "search_placeholder"].includes(labelKey) ? 300 : 12000;
    input.dir = "auto";
    input.dataset.field = key;
    input.dataset.section = sectionID;
    if (entryIndex !== undefined) input.dataset.item = entryIndex;
    if (key === "text") input.rows = 3;
    else input.type = "text";
    if (target.kind === "form" && key === "title") input.maxLength = 200;
    input.addEventListener("input", () => {
      remember();
      const before = target[key];
      target[key] = input.value;
      if (target.kind === "form" && key === "title") followTitle(target, before);
      refreshNativeEligibility(sectionID);
      changed();
    });
    wrap.append(input);
 if(key==="image"&&root.dataset.canPickMedia==="true") {const id=`builder-image-${sectionID}-${entryIndex??"section"}`;imageTargets.set(id,input);const choose=button("choose_image",()=>window.dispatchEvent(new CustomEvent("media:open",{detail:{target:id}})));wrap.append(choose);}
 parent.append(wrap);
  }
  const nativeKinds = ["hero", "features", "steps", "comparison", "faq", "product_preview", "media_text", "switcher", "directory", "task_entry"];
  const nativeOnlyKinds = ["steps", "comparison", "product_preview", "media_text", "switcher", "directory", "task_entry"];
  // Entry images stay on these kinds; task choices use text, links and icons.
  const entryImageKinds = ["features", "steps", "media_text", "switcher"];
  function nativeVariants(kind) {
    if (kind === "hero") return ["default", "split", "centered"];
    if (kind === "faq") return ["default", "split"];
    if (kind === "task_entry") return ["default", "search_first", "choice_first", "service_first"];
    if (kind === "features") return ["default", "compact_rows", "image_led", "featured_secondary"];
    if (kind === "directory") return ["default", "compact"];
    if (kind === "media_text") return ["default", "image_start", "image_end"];
    return ["default"];
  }
  function nativeSwitchBlocked(section) {
    if (section.hero_scene || section.feed) return true;
    if (section.link && !section.link_label?.trim()) return true;
    if (["faq", "switcher"].includes(section.kind) && (section.items || []).some(entry => !entry.title?.trim())) return true;
    if (section.kind === "features" && (section.items || []).some(entry => entry.link && !entry.title?.trim())) return true;
    if (!["hero", "product_preview"].includes(section.kind) && (section.image || section.image_alt)) return true;
    if (["hero", "comparison", "product_preview", "directory"].includes(section.kind) && section.items?.length) return true;
    if (!nativeVariants(section.kind).includes(section.variant || "default")) return true;
    return (section.items || []).some(entry => entry.price || (section.kind !== "features" && entry.link) || (!entryImageKinds.includes(section.kind) && (entry.image || entry.image_alt)));
  }
  // Field edits do not re-render the editor, preserving focus and selection.
  // Recompute the native option from the current section instead of leaving a
  // disabled option stale after an incompatible field has been cleared.
  function refreshNativeEligibility(sectionID) {
    const section = doc.sections.find((entry) => entry.id === sectionID);
    const parent = [...list.children].find((entry) => entry.dataset.sectionId === sectionID);
    if (!section || !parent) return;
    const option = parent.querySelector('select[data-field="library"] option[value="sections"]');
    if (!option) return;
    const blocked = !section.library && nativeSwitchBlocked(section);
    option.disabled = blocked;
    const help = parent.querySelector("[data-native-eligibility]");
    if (help) help.hidden = !blocked;
  }
  function libraryControl(section, parent) {
    if (!nativeKinds.includes(section.kind)) return;
    const label = el("label", "block text-sm", text("library"));
    const select = el("select", "bg-panel border border-line p-2");
    select.dataset.field = "library";
    for (const value of ["", "sections"]) {
      const option = el("option", "", text(value ? "library_native" : "library_legacy"));
      option.value = value;
      option.disabled = value ? !section.library && nativeSwitchBlocked(section) : nativeOnlyKinds.includes(section.kind);
      select.append(option);
    }
    select.value = section.library || "";
    select.addEventListener("change", () => {
      if (!select.value && !confirm(text("confirm_library_change"))) { select.value = section.library || ""; return; }
      mutate(() => {
        section.library = select.value;
        if (section.library) section.native = section.native || {};
        else { delete section.native; for (const entry of section.items || []) { delete entry.icon; delete entry.featured; } }
      });
    });
    label.append(select); parent.append(label);
    const help = el("p", "text-muted text-sm", text("library_blocked"));
    help.dataset.nativeEligibility = "true";
    help.hidden = !!section.library || !nativeSwitchBlocked(section);
    parent.append(help);
  }
  function selectField(target, key, values, parent) {
    const label = el("label", "block text-sm", text(key));
    const select = el("select", "bg-panel border border-line p-2");
    select.dataset.field = key;
    for (const value of values) { const option = el("option", "", value || text("none")); option.value = value; select.append(option); }
    select.value = target[key] || "";
    select.addEventListener("change", () => mutate(() => target[key] = select.value));
    label.append(select); parent.append(label);
  }
  function checkbox(target, key, parent) {
    const label = el("label", "flex items-center gap-2", text(key)), input = el("input");
    input.type = "checkbox"; input.dataset.field = key; input.checked = !!target[key];
    input.addEventListener("change", () => mutate(() => target[key] = input.checked));
    label.prepend(input); parent.append(label);
  }
  function nativeFields(section, parent) {
    const options = section.native || (section.native = {});
    const group = el("fieldset", "border border-line p-3 space-y-2");
    group.append(el("legend", "font-medium", text("library_native")));
    selectField(options, "background", ["", "inherit", "none", "dots", "grid", "lines", "circles", "waves", "paper", "solid"], group);
    if (section.kind === "hero") {
      for (const key of ["eyebrow", "secondary_link", "secondary_label"]) field(options, key, group, section.id);
      (options.facts || []).forEach((fact, index) => {
        const row = el("fieldset", "border border-line p-2"); row.append(el("legend", "", text("fact")));
        field(fact, "label", row, section.id, index); field(fact, "value", row, section.id, index);
        row.append(button("remove_item", () => mutate(() => options.facts.splice(index, 1)))); group.append(row);
      });
      group.append(button("add_fact", () => mutate(() => { options.facts = options.facts || []; options.facts.push({label: text("fact"), value: "0"}); }), (options.facts || []).length >= 8));
    }
    if (["hero", "product_preview"].includes(section.kind)) {
      field(options, "caption", group, section.id);
      checkbox(options, "illustration", group);
      group.append(el("p", "text-muted text-sm", text("illustration_help")));
    }
    if (section.kind === "task_entry") {
      group.append(el("p", "text-muted text-sm", text("task_entry_help")));
      if (section.variant === "search_first") {
        const search = options.search || (options.search = {action: "", label: text("search_label_default"), param: "q"});
        for (const key of ["action", "label", "placeholder", "param"]) field(search, key, group, section.id, undefined, "search_" + key);
      }
    }
    if (section.kind === "features" && ["compact_rows", "image_led", "featured_secondary"].includes(section.variant)) field(options, "empty_text", group, section.id);
    if (section.kind === "comparison") comparisonFields(section, options, group);
    if (section.kind === "directory") {
      if (!options.list_type) options.list_type = "post";
      field(options, "list_type", group, section.id);
      group.append(el("p", "text-muted text-sm", text("list_type_help")));
    }
    parent.append(group);
  }
  function comparisonFields(section, options, parent) {
    (options.columns || []).forEach((column, index) => {
      const group = el("div", "border border-line p-2");
      field(options.columns, index, group, section.id, undefined, "column");
      if (index > 0) group.append(button("remove_column", () => mutate(() => { options.columns.splice(index, 1); for (const row of options.rows || []) row.cells.splice(index - 1, 1); }), options.columns.length <= 2));
      parent.append(group);
    });
    parent.append(button("add_column", () => mutate(() => { options.columns.push(text("column")); for (const row of options.rows || []) row.cells.push({text: "", status: ""}); }), (options.columns || []).length >= 6));
    (options.rows || []).forEach((row, index) => {
      const group = el("fieldset", "border border-line p-2 space-y-2"); group.append(el("legend", "", text("comparison_row")));
      field(row, "label", group, section.id, index);
      row.cells.forEach((cell, cellIndex) => {
        const cellGroup = el("fieldset", "border border-line p-2"); cellGroup.append(el("legend", "", options.columns[cellIndex + 1]));
        field(cell, "text", cellGroup, section.id, index); selectField(cell, "status", ["", "yes", "no"], cellGroup); group.append(cellGroup);
      });
      group.append(button("remove_item", () => mutate(() => options.rows.splice(index, 1)))); parent.append(group);
    });
    parent.append(button("add_row", () => mutate(() => { options.rows = options.rows || []; options.rows.push({label: text("comparison_row"), cells: options.columns.slice(1).map(() => ({text: "", status: ""}))}); }), (options.rows || []).length >= 24));
  }

  // Form sections. internal/builder/forms.go is the authority; these
  // mirrors only explain problems before the server rejects them.
  const formTypes = ["text", "textarea", "email", "number", "date", "choice", "checkbox", "file"];
  const formSlugPattern = /^[a-z0-9-]{1,40}$/, formNamePattern = /^[a-z0-9_]{1,40}$/;
  // asciiKey mirrors SlugFromTitle: lowercase ASCII letters and digits, any
  // other run joined by sep, capped at 40 with no trailing sep. A title with
  // no ASCII letter or digit (Arabic, say) gives "".
  function asciiKey(value, sep) {
    let out = "", gap = false;
    for (const ch of String(value || "").toLowerCase()) {
      if ((ch >= "a" && ch <= "z") || (ch >= "0" && ch <= "9")) {
        if (gap && out) out += sep;
        gap = false;
        out += ch;
      } else gap = true;
    }
    if (out.length > 40) out = out.slice(0, 40).replace(sep === "-" ? /-+$/ : /_+$/, "");
    return out;
  }
  const slugFromTitle = (title) => asciiKey(title, "-");
  const nameFromLabel = (label) => asciiKey(label, "_");
  function formErrors(section) {
    const form = section.form || {fields: []}, fields = form.fields || [];
    const slug = form.slug || "";
    let slugError = "";
    if (!slug) slugError = "form_slug_required";
    else if (!formSlugPattern.test(slug)) slugError = "form_slug_invalid";
    else if (slug === "contact" || slug === "comment") slugError = "form_slug_reserved";
    // ValidatePublication compares visible forms only.
    else if (!section.hidden && doc.sections.some((other) => other !== section && !other.hidden && other.kind === "form" && other.form?.slug === slug)) slugError = "form_slug_repeated";
    let files = 0;
    const field = fields.map((entry, index) => {
      const name = entry.name || "";
      let nameError = "";
      if (!name) nameError = "field_name_required";
      else if (!formNamePattern.test(name) || name.startsWith("_") || name === "website") nameError = "field_name_invalid";
      else if (fields.some((other, otherIndex) => otherIndex < index && other.name === name)) nameError = "field_name_repeated";
      const typeError = entry.type === "file" && ++files > 1 ? "form_file_once" : !formTypes.includes(entry.type) ? "form_field_type_unknown" : "";
      const choicesError = entry.type === "choice" && !(entry.choices || []).some((choice) => choice.trim()) ? "field_choices_required" : "";
      return {name: nameError, type: typeError, choices: choicesError};
    });
    return {slug: slugError, fields: fields.length ? "" : "form_fields_required", field};
  }
  // formsUnpublishable mirrors validateFormsForPublish: hidden form
  // sections are skipped, as ValidatePublication skips them.
  function formsUnpublishable() {
    return doc.sections.some((section) => {
      if (section.kind !== "form" || section.hidden) return false;
      const errors = formErrors(section);
      return !!(errors.slug || errors.fields || errors.field.some((entry) => entry.name || entry.type || entry.choices));
    });
  }
  // refreshFormHints updates hints and aria-invalid in place: typing never
  // re-renders the editor, so focus and selection stay put.
  function refreshFormHints() {
    for (const node of list.querySelectorAll("[data-section-id]")) {
      const section = doc.sections.find((entry) => entry.id === node.dataset.sectionId);
      if (section?.kind !== "form") continue;
      const errors = formErrors(section);
      const show = (hint, key) => {
        if (!hint) return;
        hint.hidden = !key;
        hint.textContent = key ? text(key) : "";
        const control = hint.dataset.control && node.querySelector(`[data-hint-id="${hint.id}"]`);
        if (control) {
          if (key) control.setAttribute("aria-invalid", "true");
          else control.removeAttribute("aria-invalid");
        }
      };
      show(node.querySelector('[data-form-hint="slug"]'), errors.slug);
      show(node.querySelector('[data-form-hint="fields"]'), errors.fields);
      node.querySelectorAll("fieldset[data-form-field]").forEach((group) => {
        const entry = errors.field[Number(group.dataset.formField)] || {};
        show(group.querySelector('[data-form-hint="name"]'), entry.name);
        show(group.querySelector('[data-form-hint="type"]'), entry.type);
        show(group.querySelector('[data-form-hint="choices"]'), entry.choices);
      });
    }
    buttons();
  }
  // A slug or name the author has not edited keeps following its source text.
  function followTitle(section, before) {
    const form = section.form || (section.form = {slug: "", submit_label: "", success: "", fields: []});
    if ((form.slug || "") !== slugFromTitle(before)) return;
    form.slug = slugFromTitle(section.title);
    const input = [...list.querySelectorAll('input[data-form="slug"]')].find((node) => node.dataset.section === section.id);
    if (input) input.value = form.slug;
    refreshFormHints();
  }
  function hint(parent, kind, control, id) {
    const node = el("p", "builder-form-hint");
    node.id = id;
    node.dataset.formHint = kind;
    node.hidden = true;
    if (control) {
      node.dataset.control = "true";
      control.dataset.hintId = id;
      control.setAttribute("aria-describedby", [control.getAttribute("aria-describedby"), id].filter(Boolean).join(" "));
    }
    parent.append(node);
    return node;
  }
  function formInput(target, key, parent, sectionID, options = {}) {
    const wrap = el("label", "block text-sm space-y-1", text(options.label || key));
    const input = el(options.multiline ? "textarea" : "input", "w-full bg-panel border border-line p-2");
    if (options.multiline) input.rows = 3;
    else input.type = "text";
    input.value = options.value ?? (target[key] || "");
    input.maxLength = options.max;
    input.dir = options.ltr ? "ltr" : "auto";
    input.dataset.form = key;
    input.dataset.section = sectionID;
    if (options.focusKey) input.dataset.focusKey = options.focusKey;
    input.addEventListener("input", () => {
      remember();
      if (options.onInput) options.onInput(input.value);
      else target[key] = input.value;
      refreshFormHints();
      changed();
    });
    wrap.append(input);
    parent.append(wrap);
    return input;
  }
  function uniqueName(fields, base) {
    if (!base || !fields.some((entry) => entry.name === base)) return base;
    for (let n = 2; ; n++) {
      const candidate = `${base.slice(0, 37)}_${n}`;
      if (!fields.some((entry) => entry.name === candidate)) return candidate;
    }
  }
  function formFields(section, parent) {
    const form = section.form || (section.form = {slug: slugFromTitle(section.title), submit_label: "", success: "", fields: []});
    form.fields = form.fields || [];
    const group = el("fieldset", "builder-form border border-line p-3 space-y-2");
    group.append(el("legend", "font-medium", text("form_settings")));
    const slug = formInput(form, "slug", group, section.id, {label: "form_slug", max: 40, ltr: true});
    slug.setAttribute("aria-describedby", `builder-form-${section.id}-slug-help`);
    const slugHelp = el("p", "text-muted text-sm", text("form_slug_help"));
    slugHelp.id = `builder-form-${section.id}-slug-help`;
    group.append(slugHelp);
    hint(group, "slug", slug, `builder-form-${section.id}-slug-hint`);
    formInput(form, "submit_label", group, section.id, {max: 200});
    formInput(form, "success", group, section.id, {max: 1000, multiline: true});
    const fieldList = el("div", "space-y-2");
    fieldList.append(el("h3", "font-medium", text("form_fields")));
    const files = form.fields.filter((entry) => entry.type === "file").length;
    form.fields.forEach((entry, index) => {
      const prefix = `builder-form-${section.id}-${index}`, key = (part, at = index) => `${section.id}:field:${at}:${part}`;
      const row = el("fieldset", "border border-line p-3 space-y-2");
      row.dataset.formField = index;
      row.append(el("legend", "", `${text("form_field")} ${index + 1}`));
      formInput(entry, "label", row, section.id, {label: "field_label", max: 200, focusKey: key("label"), onInput: (value) => {
        const name = row.querySelector('input[data-form="name"]');
        // The name follows the label until edited; a new field's field_N
        // default counts as unedited while its label is still "Field N".
        const name0 = entry.name || "";
        if (name0 === nameFromLabel(entry.label) || (/^field_\d+$/.test(name0) && entry.label === `${text("form_field")} ${name0.slice(6)}`)) {
          entry.name = nameFromLabel(value);
          if (name) name.value = entry.name;
        }
        entry.label = value;
      }});
      const name = formInput(entry, "name", row, section.id, {label: "field_name", max: 40, ltr: true, focusKey: key("name")});
      hint(row, "name", name, `${prefix}-name-hint`);
      const typeLabel = el("label", "block text-sm", text("field_type"));
      const type = el("select", "bg-panel border border-line p-2");
      type.dataset.form = "type";
      type.dataset.focusKey = key("type");
      for (const value of formTypes) {
        const option = el("option", "", text("type_" + value));
        option.value = value;
        option.disabled = value === "file" && entry.type !== "file" && files > 0;
        type.append(option);
      }
      type.value = entry.type || "text";
      type.addEventListener("change", () => mutate(() => {
        entry.type = type.value;
        if (entry.type === "choice") entry.choices = entry.choices || [];
      }, key("type")));
      typeLabel.append(type);
      row.append(typeLabel);
      hint(row, "type", type, `${prefix}-type-hint`);
      const requiredLabel = el("label", "builder-form-check flex items-center gap-2", text("field_required"));
      const required = el("input");
      required.type = "checkbox";
      required.dataset.form = "required";
      required.dataset.focusKey = key("required");
      required.checked = !!entry.required;
      required.addEventListener("change", () => mutate(() => entry.required = required.checked, key("required")));
      requiredLabel.prepend(required);
      row.append(requiredLabel);
      formInput(entry, "help", row, section.id, {label: "field_help", max: 500, focusKey: key("help")});
      if (entry.type === "choice") {
        const choices = formInput(entry, "choices", row, section.id, {label: "field_choices", multiline: true, max: 4200, value: (entry.choices || []).join("\n"), onInput: (value) => {
          // The server caps a choice at 200 characters and a field at 20.
          const lines = value.split("\n").map((line) => line.trim().slice(0, 200)).filter(Boolean);
          entry.choices = lines.slice(0, 20);
          row.querySelector("[data-choices-limit]").hidden = lines.length <= 20;
        }});
        hint(row, "choices", choices, `${prefix}-choices-hint`);
        const limit = el("p", "builder-form-hint", text("field_choices_limit"));
        limit.dataset.choicesLimit = "true";
        limit.hidden = true;
        row.append(limit);
      }
      const actions = el("div", "flex flex-wrap gap-1");
      // The moved field keeps focus: on the same button, or the other one
      // when it reached an end.
      const move = (to, part) => () => mutate(() => {
        [form.fields[to], form.fields[index]] = [form.fields[index], form.fields[to]];
      }, key(part, to), key(part === "up" ? "down" : "up", to));
      const up = button("field_up", move(index - 1, "up"), index === 0);
      const down = button("field_down", move(index + 1, "down"), index === form.fields.length - 1);
      up.dataset.focusKey = key("up");
      down.dataset.focusKey = key("down");
      actions.append(up, down, button("remove_field", () => mutate(() => form.fields.splice(index, 1))));
      row.append(actions);
      fieldList.append(row);
    });
    hint(fieldList, "fields", null, `builder-form-${section.id}-fields-hint`);
    fieldList.append(button("add_field", () => mutate(() => {
      const label = `${text("form_field")} ${form.fields.length + 1}`;
      // field_N in every locale: an Arabic "حقل N" would derive just "N".
      form.fields.push({name: uniqueName(form.fields, `field_${form.fields.length + 1}`), label, type: "text", required: false, help: "", choices: []});
    }), form.fields.length >= 30));
    group.append(fieldList);
    parent.append(group);
  }
  function suggestionCurrent(original) {
    return doc.sections.find(section => section.id === original.id);
  }
  async function suggestionRequest(original, mode, binding = "") {
    const response = await fetch(`/admin/builder/${encodeURIComponent(root.dataset.id)}/suggest`, {
      method: "POST", headers: {"Content-Type": "application/json", "X-CSRF-Token": csrf},
      body: JSON.stringify({section: original, mode, ai_binding: binding}),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "suggest_invalid");
    return result;
  }
  async function previewSuggestion(original, candidate, message) {
    if (JSON.stringify(suggestionCurrent(original)) !== JSON.stringify(original)) {message.textContent = text("suggest_stale"); return;}
    clearTimeout(previewTimer);
    const sequence = ++previewSerial;
    const proposed = copy(doc);
    proposed.sections[proposed.sections.findIndex(section => section.id === original.id)] = copy(candidate.section);
    try {
      const response = await request("preview", {document: proposed});
      const html = await response.text();
      if (sequence === previewSerial) {frame.srcdoc = html; message.textContent = text("suggest_preview_notice");}
    } catch {message.textContent = text("preview_error");}
  }
  async function applySuggestion(original, candidate, message) {
    clearTimeout(timer);
    await pending.catch(() => {});
    const index = doc.sections.findIndex(section => section.id === original.id);
    if (blocked || index < 0 || JSON.stringify(doc.sections[index]) !== JSON.stringify(original)) {message.textContent = text("suggest_stale"); return;}
    clearTimeout(timer); clearTimeout(previewTimer);
    remember();
    doc.sections[index] = copy(candidate.section);
    dirty = true; serial++;
    render(); buttons(); notify("suggest_applied");
    [...list.children].find(node => node.dataset.sectionId === original.id)?.focus();
    await preview();
  }
  function suggestionCards(original, result, target, message) {
    target.replaceChildren();
    for (const candidate of result.candidates) {
      const card = el("div", "border border-line p-3 space-y-2");
      card.append(el("h3", "font-semibold", `${config.defaults[candidate.section.kind]?.title || candidate.section.kind}: ${text(candidate.label_key)}`));
      card.append(el("p", "text-sm", text(candidate.reason_key)));
      card.append(button("suggest_preview", () => previewSuggestion(original, candidate, message)), button("suggest_apply", () => applySuggestion(original, candidate, message)));
      target.append(card);
    }
    if (!result.candidates.length) target.append(el("p", "text-sm", text("suggest_empty")));
  }
  async function openSuggestions(section, article) {
    const original = copy(section);
    article.querySelector("[data-suggestions]")?.remove();
    const panel = el("details", "border border-line p-3 space-y-3"); panel.open = true; panel.dataset.suggestions = "true";
    panel.append(el("summary", "font-semibold cursor-pointer", text("suggest_improvement")));
    const source = el("p", "text-sm", text("suggest_local_source"));
    const message = el("p", "text-sm", text("suggest_loading")); message.setAttribute("role", "status");
    const cards = el("div", "space-y-2"); panel.append(source, message, cards); article.insertBefore(panel, article.children[1] || null);
    try {
      const result = await suggestionRequest(original, "local");
      if (!panel.isConnected) return;
      message.textContent = ""; suggestionCards(original, result, cards, message);
      if (result.ai_destination && result.ai_binding && result.candidates.length) {
        panel.append(el("p", "text-sm", `${text("suggest_ai_disclosure")} ${result.ai_destination}`));
        const ai = button("suggest_ai", async () => {
          if (JSON.stringify(suggestionCurrent(original)) !== JSON.stringify(original)) {message.textContent = text("suggest_stale"); return;}
          ai.disabled = true; message.textContent = text("suggest_loading");
          try {
            const selected = await suggestionRequest(original, "ai", result.ai_binding);
            if (!panel.isConnected) return;
            source.textContent = text("suggest_ai_source"); message.textContent = ""; suggestionCards(original, selected, cards, message);
          } catch (error) {message.textContent = text(labels[error.message] ? error.message : "suggest_invalid");}
          finally {ai.disabled = false;}
        }); panel.append(ai);
      } else panel.append(el("p", "text-muted text-sm", text(result.candidates.length ? "suggest_ai_unavailable" : "suggest_ai_no_candidates")));
    } catch (error) {message.textContent = text(labels[error.message] ? error.message : "suggest_invalid");}
  }

  function render() {
    imageTargets.clear();
 list.replaceChildren();
    if (!doc.sections.length) {
      list.append(el("p", "text-muted p-4", text("empty")));
    }
    doc.sections.forEach((section, index) => {
      const article = el(
        "section",
        "border border-line bg-panel p-3 space-y-3",
      );
      article.dataset.sectionId = section.id;
      article.tabIndex = -1;
      const heading = el(
        "div",
        "flex flex-wrap items-center justify-between gap-2",
      );
      const title = el(
        "h2",
        "font-bold",
        `${index + 1}. ${config.defaults[section.kind]?.title || section.kind}`,
      );
      title.id = `builder-heading-${section.id}`;
      article.setAttribute("aria-labelledby", title.id);
      const handle = el("span", "builder-drag", "⠿");
      handle.title = text("drag");
      handle.setAttribute("aria-hidden", "true");
      const caption = el("div", "builder-caption");
      caption.append(handle, title);
      heading.append(caption);
      const actions = el("div", "flex flex-wrap gap-1");
      actions.append(
        button("up", () =>
          mutate(() => {
            [doc.sections[index - 1], doc.sections[index]] = [
              doc.sections[index],
              doc.sections[index - 1],
            ];
          }), index === 0),
        button("down", () =>
          mutate(() => {
            [doc.sections[index + 1], doc.sections[index]] = [
              doc.sections[index],
              doc.sections[index + 1],
            ];
          }), index === doc.sections.length - 1),
        button("duplicate", () =>
          mutate(() => {
            const duplicate = copy(section);
            duplicate.id = crypto.randomUUID();
            doc.sections.splice(index + 1, 0, duplicate);
          }), doc.sections.length >= 60),
        button("remove", () => {
          if (confirm(text("confirm_remove"))) {
            mutate(() => doc.sections.splice(index, 1));
          }
        }),
      );
      actions.append(button("suggest_improvement", () => openSuggestions(section, article)));
      heading.append(actions);
      article.append(heading);
      libraryControl(section, article);
      const native = section.library === "sections";
      const advanced=el("details","border border-line p-3 space-y-2");advanced.append(el("summary","cursor-pointer font-medium",text("advanced")));advanced.open=advancedSections.has(section.id);advanced.addEventListener("toggle",()=>{if(!advanced.isConnected)return;if(advanced.open)advancedSections.add(section.id);else advancedSections.delete(section.id);});
 const variantLabel = el("label", "block text-sm", text("variant")),
        variant = el("select", "bg-panel border border-line p-2");
      (native ? nativeVariants(section.kind) : ["default", "split", "centered"]).forEach((value) => {
        const option = el("option", "", text("layout_" + value));
        option.value = value;
        variant.append(option);
      });
      variant.value = section.variant || "default";
      variant.addEventListener(
        "change",
        () => mutate(() => {
          section.variant = variant.value;
          if (section.kind === "task_entry" && section.variant !== "search_first" && section.native) delete section.native.search;
          if (section.kind === "features" && section.variant === "default" && section.native) delete section.native.empty_text;
        }),
      );
      variantLabel.append(variant);
      advanced.append(variantLabel);
      if (section.kind === "hero" && !native) {
        const label = el("label", "block text-sm", text("hero_scene"));
        const select = el("select", "bg-panel border border-line p-2");
        select.dataset.field = "hero_scene";
        ["", "orbit", "ribbons", "blocks"].forEach(value => {
          const option = el("option", "", text("scene_" + (value || "off")));
          option.value = value;
          select.append(option);
        });
        select.value = section.hero_scene || "";
        select.addEventListener("change", () => mutate(() => section.hero_scene = select.value));
        label.append(select);
        advanced.append(label);
        advanced.append(el("p", "text-xs", text("scene_help")));
      }
      if (!native && (section.kind === "text" || section.kind === "projects")) {
        const sourceLabel = el("label", "block text-sm", text("source"));
        const source = el("select", "bg-panel border border-line p-2");
        source.dataset.field = "feed";
        for (
          const [value, label] of [["", "manual"], ["posts", "published_posts"]]
        ) {
          const option = el("option", "", text(label));
          option.value = value;
          source.append(option);
        }
        source.value = section.feed || "";
        source.addEventListener(
          "change",
          () => mutate(() => section.feed = source.value),
        );
        sourceLabel.append(source);
        article.append(sourceLabel);
        if (section.feed === "posts") {
          article.append(el("p", "text-muted text-sm", text("feed_help")));
        }
      }

      ["title", "text", ...(!native || ["hero", "product_preview"].includes(section.kind) ? ["image", "image_alt"] : []), "link", "link_label"].forEach(
        (key) => field(section, key, article, section.id),
      );
      if (native) nativeFields(section, article);
      if (section.kind === "form") formFields(section, article);
      article.append(advanced);
 const hideLabel = el("label", "flex items-center gap-2", text("hidden")),
        hide = el("input");
      hide.type = "checkbox";
      hide.checked = !!section.hidden;
      hide.addEventListener(
        "change",
        () => mutate(() => section.hidden = hide.checked),
      );
      hideLabel.prepend(hide);
      article.append(hideLabel);
      if (section.kind !== "form" && (!native || ["features", "steps", "faq", "media_text", "switcher", "task_entry"].includes(section.kind))) {
      (section.items || []).forEach((entry, itemIndex) => {
        const group = el("fieldset", "border border-line p-3 space-y-2");
        group.append(el("legend", "", `${text("item")} ${itemIndex + 1}`));
        (native ? section.kind === "features" ? ["title", "text", "image", "image_alt", "link"] : section.kind === "task_entry" ? ["title", "text", "link"] : entryImageKinds.includes(section.kind) ? ["title", "text", "image", "image_alt"] : ["title", "text"] : ["title", "text", "image", "image_alt", "link", "price"]).forEach(
          (key) => field(entry, key, group, section.id, itemIndex),
        );
        if (native && ["features", "task_entry"].includes(section.kind)) {
          selectField(entry, "icon", ["", "bolt", "shield", "chart", "globe", "clock", "check", "layers"], group);
          if (section.kind === "features") checkbox(entry, "featured", group);
        }
        group.append(
          button(
            "remove_item",
            () => mutate(() => section.items.splice(itemIndex, 1)),
          ),
        );
        article.append(group);
      });
      article.append(button("add_item", () =>
        mutate(() => {
          section.items = section.items || [];
          section.items.push({
            title: native && section.kind === "faq" ? text("question_default") : "",
            text: "",
            image: "",
            image_alt: "",
            link: "",
            price: "",
          });
        }), (section.items || []).length >= 24));
      }
      list.append(article);
    });
    refreshFormHints();
  }
  async function request(action, body) {
    const response = await fetch(
      `/admin/builder/${encodeURIComponent(root.dataset.id)}/${action}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify(body),
      },
    );
    if (!response.ok) {
      const error = new Error(`Builder ${action}: ${response.status}`);
      if (response.status === 409) {
        blocked = true;
        notify("conflict", true);
      } else if (action !== "preview") {
        const body = await response.json().catch(() => null);
        if (body?.error === "builder.form_slug_taken" && typeof body.slug === "string") {
          error.slug = body.slug;
          status.textContent = text("form_slug_taken").replace("{slug}", body.slug);
          status.setAttribute("role", "alert");
        } else notify("error", true);
      }
      throw error;
    }
    return response;
  }
  function save() {
    clearTimeout(timer);
    pending = pending.catch(() => {}).then(async () => {
      if (blocked) throw new Error("Conflict requires reloading");
      if (!dirty) return;
      const snapshot = copy(doc), savedSerial = serial;
      notify("saving");
      const response = await request("save", {
        document: snapshot,
        base_updated_at: token,
      });
      const result = await response.json();
      token = result.updated_at;
      dirty = serial !== savedSerial;
      notify(dirty ? "saving" : "saved");
      if (dirty) {
        clearTimeout(timer);
        timer = setTimeout(() => save().catch(() => {}), 900);
      }
    }).catch((error) => {
      notify(blocked ? "conflict" : "error", true);
      throw error;
    });
    return pending;
  }
  async function preview() {
    const sequence = ++previewSerial;
    frame.setAttribute("aria-busy", "true");
    try {
      const response = await request("preview", { document: copy(doc) });
      const html = await response.text();
      if (sequence === previewSerial) {
        frame.srcdoc = html;
        document.getElementById("builder-preview-status").textContent = "";
      }
    } catch {
      if (sequence === previewSerial) {
        frame.srcdoc = "";
        document.getElementById("builder-preview-status").textContent = text(
          "preview_error",
        );
      }
    } finally {
      if (sequence === previewSerial) frame.setAttribute("aria-busy", "false");
    }
  }
  root.addEventListener("click", async (event) => {
    const action = event.target.closest("[data-action]")?.dataset.action;
    if (!action || publishing) return;
    if (action === "export") {
      const blob = new Blob([JSON.stringify(doc, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `gorun-layout-${root.dataset.id}.json`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    if (action === "import") document.getElementById("builder-import").click();
    if (action === "add" && doc.sections.length < 60) {
      const kind = document.getElementById("builder-kind").value;
      mutate(() => {
        const section = copy(config.defaults[kind]);
        section.id = crypto.randomUUID();
        doc.sections.push(section);
      });
    }
    if (action === "undo" && history.length) {
      future.push(copy(doc));
      doc = history.pop();
      render();
      changed();
    }
    if (action === "redo" && future.length) {
      history.push(copy(doc));
      doc = future.pop();
      render();
      changed();
    }
    if (action === "desktop" || action === "mobile") {
      frame.style.maxWidth = action === "mobile" ? "390px" : "100%";
      root.querySelector('[data-action="desktop"]').setAttribute(
        "aria-pressed",
        String(action === "desktop"),
      );
      root.querySelector('[data-action="mobile"]').setAttribute(
        "aria-pressed",
        String(action === "mobile"),
      );
    }
    if (action === "save") {
      try {
        await save();
      } catch {}
    }
    if (action === "publish" && confirm(text("confirm_publish"))) {
      let takenSlug;
      publishing = true;
      clearTimeout(timer);
      const controls = [
        ...root.querySelectorAll("input,textarea,select,button"),
      ].map((node) => [node, node.disabled]);
      controls.forEach(([node]) => node.disabled = true);
      try {
        do {
          await save();
        } while (dirty);
        const response = await request("publish", { updated_at: token });
        const result = await response.json();
        token = result.updated_at;
        contentToken = result.content_token;
        notify("published");
      } catch (error) {
        if (error.slug !== undefined) takenSlug = error.slug;
        else notify(blocked ? "conflict" : "error", true);
      } finally {
        publishing = false;
        controls.forEach(([node, disabled]) => node.disabled = disabled);
        buttons();
      }
      if (takenSlug !== undefined) {
        [...list.querySelectorAll('input[data-form="slug"]')].find((node) => node.value === takenSlug)?.focus();
      }
    }
    if (action === "reset" && confirm(text("confirm_reset"))) {
      publishing = true;
      clearTimeout(timer);
      clearTimeout(previewTimer);
      const controls = [
        ...root.querySelectorAll("input,textarea,select,button"),
      ].map((node) => [node, node.disabled]);
      controls.forEach(([node]) => node.disabled = true);
      try {
        await pending.catch(() => {});
        if (token === config.draft.base_updated_at) {
          dirty = false;
          window.location.reload();
          return;
        }
        const response = await request("reset", { updated_at: token, expected_content_token: contentToken });
        const result = await response.json();
        token = result.updated_at;
        contentToken = result.content_token;
        doc = result.document;
        history.length = 0;
        future.length = 0;
        dirty = false;
        blocked = false;
        serial++;
        render();
        await preview();
        notify("discarded");
        window.location.reload();
      } catch {
        notify(blocked ? "conflict" : "error", true);
      } finally {
        publishing = false;
        controls.forEach(([node, disabled]) => node.disabled = disabled);
        buttons();
      }
    }
  });
  document.getElementById("builder-import").addEventListener(
    "change",
    async (event) => {
      const file = event.target.files[0];
      event.target.value = "";
      if (!file || publishing) return;
      if (blocked) {
        notify("conflict", true);
        return;
      }
      publishing = true;
      const controls = [
        ...root.querySelectorAll("input,textarea,select,button"),
      ].map((node) => [node, node.disabled]);
      controls.forEach(([node]) => node.disabled = true);
      notify("importing");
      try {
        if (file.size >= (1 << 20)) throw new Error("Layout file too large");
        const candidate = JSON.parse(await file.text());
        if (
          !candidate || candidate.version !== 1 ||
          !Array.isArray(candidate.sections)
        ) throw new Error("Invalid layout shape");
        await request("preview", { document: candidate });
        mutate(() => doc = copy(candidate));
      } catch {
        notify("import_error", true);
      } finally {
        publishing = false;
        controls.forEach(([node, disabled]) => node.disabled = disabled);
        buttons();
      }
    },
  );
  window.addEventListener("beforeunload", (event) => {
    if (dirty) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
  const acknowledgement = document.getElementById("builder-convert"),
    publishButton = root.querySelector('[data-action="publish"]');
  if (acknowledgement && publishButton) {
    publishButton.disabled = true;
    acknowledgement.addEventListener(
      "change",
      () => buttons(),
    );
  }
  render();
  preview();
  if (window.Sortable) {
    new Sortable(list, {
      handle: ".builder-drag",
      draggable: "[data-section-id]",
      animation: 0,
      delay: 120,
      delayOnTouchOnly: true,
      touchStartThreshold: 4,
      fallbackTolerance: 4,
      ghostClass: "builder-sort-ghost",
      chosenClass: "builder-sort-chosen",
      onEnd: (event) => {
        // DOM indices can include empty states or drag clones; IDs are the model key.
        const sectionID = event.item.dataset.sectionId;
        const from = doc.sections.findIndex((section) => section.id === sectionID);
        const to = event.newDraggableIndex;
        if (from < 0 || !Number.isInteger(to) || to < 0 || to >= doc.sections.length) {
          render();
          return;
        }
        if (from === to) return;
        mutate(() => {
          const [section] = doc.sections.splice(from, 1);
          doc.sections.splice(to, 0, section);
        });
        [...list.children].find((node) => node.dataset.sectionId === sectionID)?.focus();
      },
    });
  }
})();
