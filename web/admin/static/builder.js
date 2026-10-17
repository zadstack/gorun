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
    const publish = root.querySelector('[data-action="publish"]');
    const acknowledge = document.getElementById("builder-convert");
    if (publish) {
      publish.disabled = doc.sections.length === 0 ||
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
  function mutate(fn) {
    const focused = document.activeElement.closest("[data-section-id]")?.dataset
      .sectionId;
    remember();
    fn();
    render();
    if (focused) {
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
  function field(target, key, parent, sectionID, entryIndex) {
    const wrap = el("label", "block text-sm space-y-1", text(key));
    const input = el(
      key === "text" ? "textarea" : "input",
      "w-full bg-panel border border-line p-2",
    );
    input.value = target[key] || "";
    input.maxLength = key === "link" || key === "image" ? 2048 : 12000;
    input.dir = "auto";
    input.dataset.field = key;
    input.dataset.section = sectionID;
    if (entryIndex !== undefined) input.dataset.item = entryIndex;
    if (key === "text") input.rows = 3;
    else input.type = "text";
    input.addEventListener("input", () => {
      remember();
      target[key] = input.value;
      changed();
    });
    wrap.append(input);
    parent.append(wrap);
  }
  function render() {
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
      heading.append(actions);
      article.append(heading);
      const variantLabel = el("label", "block text-sm", text("variant")),
        variant = el("select", "bg-panel border border-line p-2");
      ["default", "split", "centered"].forEach((value) => {
        const option = el("option", "", text("layout_" + value));
        option.value = value;
        variant.append(option);
      });
      variant.value = section.variant || "default";
      variant.addEventListener(
        "change",
        () => mutate(() => section.variant = variant.value),
      );
      variantLabel.append(variant);
      article.append(variantLabel);
      if (section.kind === "hero") {
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
        article.append(label);
        article.append(el("p", "text-xs", text("scene_help")));
      }
      if (section.kind === "text" || section.kind === "projects") {
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

      ["title", "text", "image", "image_alt", "link", "link_label"].forEach(
        (key) => field(section, key, article, section.id),
      );
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
      (section.items || []).forEach((entry, itemIndex) => {
        const group = el("fieldset", "border border-line p-3 space-y-2");
        group.append(el("legend", "", `${text("item")} ${itemIndex + 1}`));
        ["title", "text", "image", "image_alt", "link", "price"].forEach(
          (key) => field(entry, key, group, section.id, itemIndex),
        );
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
            title: "",
            text: "",
            image: "",
            image_alt: "",
            link: "",
            price: "",
          });
        }), (section.items || []).length >= 24));
      list.append(article);
    });
    buttons();
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
      if (response.status === 409) {
        blocked = true;
        notify("conflict", true);
      } else if (action !== "preview") notify("error", true);
      throw new Error(`Builder ${action}: ${response.status}`);
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
      } catch {
        notify(blocked ? "conflict" : "error", true);
      } finally {
        publishing = false;
        controls.forEach(([node, disabled]) => node.disabled = disabled);
        buttons();
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
