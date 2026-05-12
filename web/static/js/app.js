// dashboard front-end glue: CSRF header injection, search, theme cycling,
// drag-and-drop, app editor modal, categories editor.

(function () {
  "use strict";

  // --- CSRF ---------------------------------------------------------------
  function csrfToken() {
    return window.__CSRF__ || "";
  }

  if (window.htmx) {
    document.body.addEventListener("htmx:configRequest", function (e) {
      e.detail.headers["X-CSRF-Token"] = csrfToken();
    });
  }

  const origFetch = window.fetch;
  window.fetch = function (input, init) {
    init = init || {};
    const method = (init.method || "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD" && method !== "OPTIONS") {
      init.headers = Object.assign({}, init.headers || {}, {
        "X-CSRF-Token": csrfToken(),
      });
    }
    return origFetch(input, init);
  };

  // Convenience JSON helper. Returns parsed body on 2xx, throws on error.
  async function api(method, path, body) {
    const init = { method, headers: {} };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const resp = await fetch(path, init);
    const text = await resp.text();
    let parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch (_) {
        /* non-JSON body */
      }
    }
    if (!resp.ok) {
      // Session expired or cleared in another tab — bounce to login so the
      // user can re-auth rather than leaving the editor stuck on a 401.
      if (resp.status === 401) {
        window.location.href = "/login";
        // Throw anyway so the caller's finally{} runs cleanly.
        throw new Error("session expired");
      }
      const msg = (parsed && parsed.error) || resp.statusText || "request failed";
      const err = new Error(msg);
      err.status = resp.status;
      throw err;
    }
    return parsed;
  }

  // --- Search focus shortcut ----------------------------------------------
  document.addEventListener("keydown", function (e) {
    if (e.key !== "/") return;
    const tag = (document.activeElement && document.activeElement.tagName) || "";
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    const search = document.getElementById("search");
    if (search) {
      e.preventDefault();
      search.focus();
      search.select();
    }
  });

  // --- Alpine.js dashboard component --------------------------------------
  window.dashboardPage = function () {
    return {
      theme: localStorage.getItem("theme") || "auto",
      cycleTheme() {
        const order = ["light", "dark", "auto"];
        const next = order[(order.indexOf(this.theme) + 1) % order.length];
        this.theme = next;
        localStorage.setItem("theme", next);
        if (typeof window.applyTheme === "function") window.applyTheme(next);
      },
      filterTiles(query) {
        const q = (query || "").toLowerCase().trim();
        document.querySelectorAll("[data-app-id]").forEach((t) => {
          if (!q) {
            t.classList.remove("hidden");
            return;
          }
          const name = (t.dataset.appName || "").toLowerCase();
          const desc = (t.dataset.appDesc || "").toLowerCase();
          t.classList.toggle("hidden", !(name.includes(q) || desc.includes(q)));
        });
        document.querySelectorAll("[data-sortable]").forEach((grid) => {
          const anyVisible = Array.from(grid.children).some(
            (c) => !c.classList.contains("hidden")
          );
          const section = grid.closest("section");
          if (section) section.classList.toggle("hidden", !anyVisible);
        });
      },
    };
  };

  // --- Drag-and-drop ------------------------------------------------------
  function collectGroups() {
    return Array.from(document.querySelectorAll("[data-sortable]")).map((g) => {
      const raw = g.dataset.categoryId;
      const categoryID = !raw || raw === "0" ? null : Number(raw);
      const ids = Array.from(g.querySelectorAll("[data-app-id]")).map((el) =>
        Number(el.dataset.appId)
      );
      return { category_id: categoryID, ids };
    });
  }

  let saveTimer = null;
  function scheduleSave() {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(saveLayout, 250);
  }
  async function saveLayout() {
    try {
      await api("POST", "/api/apps/reorder", { groups: collectGroups() });
    } catch (err) {
      console.error("reorder failed:", err);
    }
  }

  document.addEventListener("DOMContentLoaded", function () {
    if (!window.Sortable) return;
    document.querySelectorAll("[data-sortable]").forEach((grid) => {
      Sortable.create(grid, {
        group: "dashboard-apps",
        handle: "[data-drag-handle]",
        animation: 200,
        ghostClass: "opacity-40",
        chosenClass: "ring-2",
        dragClass: "shadow-lg",
        onEnd: scheduleSave,
      });
    });
  });

  // --- App editor modal ---------------------------------------------------
  window.appEditor = function () {
    return {
      open: false,
      mode: "create", // "create" | "edit"
      saving: false,
      error: "",
      id: 0,
      categories: [],
      iconPreview: "",
      iconFile: null,
      form: emptyForm(),

      async onOpen(detail) {
        this.error = "";
        this.iconFile = null;
        this.iconPreview = "";
        this.mode = detail.mode || "create";
        await this.loadCategories();
        if (this.mode === "edit" && detail.el) {
          this.fromTile(detail.el);
        } else {
          this.id = 0;
          this.form = emptyForm();
        }
        this.open = true;
      },

      close() {
        this.open = false;
      },

      fromTile(el) {
        this.id = Number(el.dataset.appId) || 0;
        this.form = {
          name: el.dataset.appName || "",
          url: el.dataset.appUrl || "",
          description: el.dataset.appDesc || "",
          icon_path: el.dataset.appIcon || "",
          category_id: el.dataset.appCategory || "",
          health_check_enabled: el.dataset.appHcEnabled === "1",
          health_check_url: el.dataset.appHcUrl || "",
        };
        this.iconPreview = this.form.icon_path
          ? "/uploads/icons/" + this.form.icon_path
          : "";
      },

      async loadCategories() {
        try {
          const data = await api("GET", "/api/categories");
          this.categories = data.categories || [];
        } catch (err) {
          // Non-fatal: editor still works with "Uncategorized" only.
          console.error("loadCategories:", err);
          this.categories = [];
        }
      },

      onIconChange(event) {
        const f = event.target.files && event.target.files[0];
        if (!f) return;
        this.iconFile = f;
        this.iconPreview = URL.createObjectURL(f);
      },

      clearIcon() {
        this.iconFile = null;
        this.iconPreview = "";
        this.form.icon_path = "";
      },

      async save() {
        if (this.saving) return;
        this.error = "";
        this.saving = true;
        try {
          if (this.iconFile) {
            const fd = new FormData();
            fd.append("file", this.iconFile);
            const resp = await fetch("/api/uploads/icon", {
              method: "POST",
              body: fd,
            });
            if (!resp.ok) {
              const t = await resp.text();
              throw new Error("Icon upload failed: " + (t || resp.statusText));
            }
            const j = await resp.json();
            this.form.icon_path = j.filename;
          }

          const payload = {
            name: this.form.name,
            url: this.form.url,
            description: this.form.description,
            icon_path: this.form.icon_path,
            category_id: this.form.category_id ? Number(this.form.category_id) : 0,
            health_check_enabled: !!this.form.health_check_enabled,
            health_check_url: this.form.health_check_url || "",
          };

          if (this.mode === "create") {
            await api("POST", "/api/apps", payload);
          } else {
            await api("PATCH", "/api/apps/" + this.id, payload);
          }
          window.location.reload();
        } catch (err) {
          this.error = err.message || "Save failed";
        } finally {
          this.saving = false;
        }
      },

      async remove() {
        if (!this.id) return;
        if (!window.confirm("Delete this app?")) return;
        try {
          await api("DELETE", "/api/apps/" + this.id);
          window.location.reload();
        } catch (err) {
          this.error = err.message || "Delete failed";
        }
      },
    };
  };

  function emptyForm() {
    return {
      name: "",
      url: "",
      description: "",
      icon_path: "",
      category_id: "",
      health_check_enabled: false,
      health_check_url: "",
    };
  }

  // --- Categories editor --------------------------------------------------
  window.categoriesEditor = function () {
    return {
      open: false,
      categories: [],
      newName: "",
      error: "",

      async onOpen() {
        this.error = "";
        await this.reload();
        this.open = true;
      },
      close() {
        this.open = false;
      },

      async reload() {
        try {
          const data = await api("GET", "/api/categories");
          this.categories = data.categories || [];
        } catch (err) {
          this.error = err.message || "Failed to load";
        }
      },

      async add() {
        if (!this.newName.trim()) return;
        try {
          await api("POST", "/api/categories", { name: this.newName.trim() });
          this.newName = "";
          await this.reload();
        } catch (err) {
          this.error = err.message || "Could not add category";
        }
      },

      async rename(c, newName) {
        const trimmed = (newName || "").trim();
        if (!trimmed || trimmed === c.name) {
          return;
        }
        try {
          await api("PATCH", "/api/categories/" + c.id, { name: trimmed });
          await this.reload();
        } catch (err) {
          this.error = err.message || "Rename failed";
          await this.reload(); // revert input
        }
      },

      async remove(c) {
        if (!window.confirm(`Delete category "${c.name}"? Its apps move to Uncategorized.`)) return;
        try {
          await api("DELETE", "/api/categories/" + c.id);
          await this.reload();
        } catch (err) {
          this.error = err.message || "Delete failed";
        }
      },
    };
  };
})();
