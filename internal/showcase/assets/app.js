/* showcase-axi live page: conversation polling, annotations, selection
   comments, device widths, and the mermaid fallback. Endpoints are relative,
   so everything resolves under /s/<id>/. */
(function () {
  "use strict";

  var contentEl = document.getElementById("content");
  var messagesEl = document.getElementById("messages");
  var endBanner = document.getElementById("end-banner");
  var composerText = document.getElementById("composer-text");
  var sendBtn = document.getElementById("send");
  var sendEndBtn = document.getElementById("send-end");
  var annotateBtn = document.getElementById("annotate-toggle");
  var overlay = document.getElementById("annotate-overlay");
  var composerError = document.getElementById("composer-error");
  var annotating = false;
  var pollTimer = null;
  var selectionBtn = null;

  /* Conversation */

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  var lastSignature = "";

  function renderState(state) {
    var entries = [];
    (state.messages || []).forEach(function (m) {
      entries.push({ at: m.created_at, role: m.role, text: m.text });
    });
    (state.feedback || []).forEach(function (f) {
      entries.push({ at: f.created_at, role: "user", text: f.text, tag: f.type, quote: f.quote, note: f.selector || f.context });
    });
    entries.sort(function (a, b) { return a.at < b.at ? -1 : 1; });

    // Skip the DOM rewrite while nothing changed so the panel is stable
    // for the reviewer between deliveries.
    var signature = entries.length + ":" + (entries.length ? entries[entries.length - 1].at : "") + ":" + (state.ended_by || "");
    if (signature === lastSignature) return;
    lastSignature = signature;

    messagesEl.textContent = "";
    if (entries.length === 0) {
      messagesEl.appendChild(el("p", "empty", "No feedback yet."));
    }
    entries.forEach(function (entry) {
      var bubble = el("div", "msg " + (entry.role === "agent" ? "agent" : "user"));
      if (entry.tag && entry.tag !== "message") bubble.appendChild(el("span", "tag", entry.tag));
      if (entry.quote) bubble.appendChild(el("blockquote", "", entry.quote));
      bubble.appendChild(el("div", "text", entry.text));
      if (entry.note) bubble.appendChild(el("div", "tag", entry.note));
      messagesEl.appendChild(bubble);
    });
    messagesEl.scrollTop = messagesEl.scrollHeight;

    if (state.ended_by) {
      endBanner.hidden = false;
      endBanner.textContent = state.ended_by === "user"
        ? "The reviewer ended this session. The agent will pick up any queued feedback once."
        : "The agent ended this session.";
      if (composerText) composerText.disabled = true;
      if (sendBtn) sendBtn.disabled = true;
      if (sendEndBtn) sendEndBtn.disabled = true;
      if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
    }
  }

  function refresh() {
    fetch("state").then(function (res) {
      if (!res.ok) throw new Error("state " + res.status);
      return res.json();
    }).then(renderState).catch(function () { /* server restarted; retry next tick */ });
  }

  function clearError() {
    if (composerError) composerError.hidden = true;
  }

  function showError(err) {
    if (!composerError) return;
    composerError.textContent = "Couldn't send: " + (err && err.message ? err.message : err);
    composerError.hidden = false;
  }

  function ensureOk(res) {
    if (res.ok) return res;
    return res.text().then(function (text) {
      throw new Error(res.status + (text ? ": " + text : ""));
    });
  }

  function postFeedback(body) {
    clearError();
    return fetch("feedback", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    }).then(ensureOk).then(refresh);
  }

  if (sendBtn) {
    sendBtn.addEventListener("click", function () {
      var text = composerText.value.trim();
      if (!text) return;
      composerText.value = "";
      postFeedback({ type: "message", text: text }).catch(showError);
    });
    composerText.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) sendBtn.click();
    });
  }

  if (sendEndBtn) {
    sendEndBtn.addEventListener("click", function () {
      var text = composerText.value.trim();
      clearError();
      var chain = text ? postFeedback({ type: "message", text: text }) : Promise.resolve();
      composerText.value = "";
      chain.then(function () {
        return fetch("end", { method: "POST" }).then(ensureOk);
      }).then(refresh).catch(showError);
    });
  }

  /* Annotation popover */

  function closePopover() {
    var existing = document.querySelector(".sc-popover");
    if (existing) existing.remove();
  }

  function openPopover(x, y, hint, onSave) {
    closePopover();
    var pop = el("div", "sc-popover");
    pop.style.left = Math.min(x, window.innerWidth - 320) + "px";
    pop.style.top = Math.min(y, window.innerHeight - 190) + "px";
    if (hint) pop.appendChild(el("p", "hint", hint));
    var area = el("textarea");
    area.rows = 3;
    area.placeholder = "What should the agent change here?";
    pop.appendChild(area);
    var actions = el("div", "actions");
    var cancel = el("button", "", "Cancel");
    var save = el("button", "", "Queue prompt");
    actions.appendChild(cancel);
    actions.appendChild(save);
    pop.appendChild(actions);
    cancel.addEventListener("click", closePopover);
    save.addEventListener("click", function () {
      var text = area.value.trim();
      if (!text) return;
      onSave(text);
      closePopover();
    });
    document.body.appendChild(pop);
    area.focus();
  }

  function selectorFor(node) {
    if (node.id) return "#" + node.id;
    var parts = [];
    while (node && node !== contentEl && node.tagName) {
      var tag = node.tagName.toLowerCase();
      var parent = node.parentElement;
      if (parent) {
        var same = Array.prototype.filter.call(parent.children, function (c) {
          return c.tagName === node.tagName;
        });
        if (same.length > 1) tag += ":nth-of-type(" + (same.indexOf(node) + 1) + ")";
      }
      parts.unshift(tag);
      node = parent;
    }
    return parts.join(" > ");
  }

  function snippet(node) {
    return (node.textContent || "").replace(/\s+/g, " ").trim().slice(0, 200);
  }

  if (annotateBtn) {
    annotateBtn.addEventListener("click", function () {
      annotating = !annotating;
      document.body.classList.toggle("annotating", annotating);
      if (overlay) overlay.hidden = !annotating;
      if (!annotating) closePopover();
    });
  }

  /* Element annotations on server-rendered content (markdown, diff, csv). */

  var annotateSelector = "p, h1, h2, h3, h4, h5, li, pre, table, tr, .dl, .diff-file, blockquote";

  contentEl.addEventListener("mouseover", function (event) {
    if (!annotating || !event.target.closest) return;
    Array.prototype.forEach.call(contentEl.querySelectorAll(".sc-hl"), function (node) {
      node.classList.remove("sc-hl");
    });
    var hit = event.target.closest(annotateSelector);
    if (hit && hit.closest("#content")) hit.classList.add("sc-hl");
  });
  contentEl.addEventListener("click", function (event) {
    if (!annotating || !event.target.closest) return;
    event.preventDefault();
    event.stopPropagation();
    var target = event.target.closest(annotateSelector) || event.target;
    target.classList.remove("sc-hl");
    target.classList.add("sc-marked");
    openPopover(event.clientX, event.clientY, snippet(target), function (text) {
      postFeedback({ type: "annotation", text: text, selector: selectorFor(target), quote: snippet(target) }).catch(showError);
    });
  }, true);

  /* Element annotations on an HTML artifact: the overlay captures the click
     and records viewport-relative coordinates, keeping the mock untouched. */

  if (overlay) {
    overlay.addEventListener("click", function (event) {
      var wrap = document.getElementById("frame-wrap");
      var rect = wrap.getBoundingClientRect();
      var x = Math.round(event.clientX - rect.left);
      var y = Math.round(event.clientY - rect.top);
      openPopover(event.clientX, event.clientY, "artifact at (" + x + ", " + y + ") in a " + Math.round(rect.width) + "px viewport", function (text) {
        postFeedback({ type: "annotation", text: text, context: "viewport " + Math.round(rect.width) + "px @ (" + x + ", " + y + ")" }).catch(showError);
      });
    });
  }

  /* Text-selection comments on server-rendered content. */

  function hideSelectionBtn() {
    if (selectionBtn) { selectionBtn.remove(); selectionBtn = null; }
  }

  document.addEventListener("mouseup", function () {
    setTimeout(function () {
      hideSelectionBtn();
      if (annotating) return;
      var sel = window.getSelection();
      if (!sel || sel.isCollapsed || !contentEl.contains(sel.anchorNode)) return;
      var quote = sel.toString().trim();
      if (!quote) return;
      var rect = sel.getRangeAt(0).getBoundingClientRect();
      selectionBtn = el("button", "", "Comment on selection");
      selectionBtn.id = "selection-btn";
      selectionBtn.style.left = rect.left + "px";
      selectionBtn.style.top = (rect.bottom + 6) + "px";
      selectionBtn.addEventListener("click", function () {
        hideSelectionBtn();
        openPopover(rect.left, rect.bottom + 6, quote.slice(0, 140), function (text) {
          postFeedback({ type: "selection", text: text, quote: quote.slice(0, 500) }).catch(showError);
        });
      });
      document.body.appendChild(selectionBtn);
    }, 0);
  });
  document.addEventListener("mousedown", function (event) {
    if (selectionBtn && event.target !== selectionBtn) hideSelectionBtn();
  });

  /* Device-width switcher for HTML artifacts. */

  Array.prototype.forEach.call(document.querySelectorAll(".device-switch button"), function (btn) {
    btn.addEventListener("click", function () {
      var wrap = document.getElementById("frame-wrap");
      if (wrap) wrap.style.width = btn.getAttribute("data-width");
      Array.prototype.forEach.call(btn.parentElement.children, function (sibling) {
        sibling.classList.toggle("active", sibling === btn);
      });
    });
  });

  /* Mermaid: render when the CDN arrived, otherwise show the source. */

  if (document.querySelector(".mermaid")) {
    var showFallback = function () {
      Array.prototype.forEach.call(document.querySelectorAll(".mermaid"), function (node) { node.hidden = true; });
      var fallback = document.getElementById("mermaid-fallback");
      if (fallback) fallback.hidden = false;
    };
    if (window.mermaid) {
      window.mermaid.initialize({ startOnLoad: false, theme: "dark" });
      window.mermaid.run({ querySelector: ".mermaid" }).catch(showFallback);
    } else {
      showFallback();
    }
  }

  refresh();
  pollTimer = setInterval(refresh, 2000);
})();
