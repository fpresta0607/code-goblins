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
      entries.push({ at: f.created_at, role: "user", text: f.text, tag: f.type, quote: f.quote, note: whereLabel(f.section, f.selector) || f.context });
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
      if (entry.note) bubble.appendChild(el("div", "where", entry.note));
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

  /* Annotation popover. It grows out of whatever triggered it and collapses
     back along the same path, so the link between source and surface stays
     obvious. */

  var popover = null;

  function closePopover() {
    if (!popover) return;
    var closing = popover;
    popover = null;
    closing.classList.add("closing");
    setTimeout(function () { closing.remove(); }, 200);
  }

  function openPopover(x, y, quote, where, onSave) {
    closePopover();
    var pop = el("div", "sc-popover");
    popover = pop;
    var left = Math.max(8, Math.min(x, window.innerWidth - 320));
    var top = Math.max(8, Math.min(y, window.innerHeight - 220));
    pop.style.left = left + "px";
    pop.style.top = top + "px";
    pop.style.transformOrigin = (x - left) + "px " + (y - top) + "px";
    if (quote || where) {
      var hint = el("p", "hint", quote);
      if (where) hint.appendChild(el("span", "where", where));
      pop.appendChild(hint);
    }
    var area = el("textarea");
    area.rows = 3;
    area.placeholder = "What should the agent change here?";
    pop.appendChild(area);
    var actions = el("div", "actions");
    var cancel = el("button", "quiet", "Cancel");
    var save = el("button", "primary", "Queue prompt");
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
    area.addEventListener("keydown", function (event) {
      if (event.key === "Escape") closePopover();
      if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) save.click();
    });
    document.body.appendChild(pop);
    area.focus();
  }

  /* Anchoring. A queued prompt has to be findable again in the source, so
     every annotation carries the element path and the nearest heading or
     section header above it. The same two functions run inside the sandboxed
     frame (see frame-helper.js); they cannot be shared across documents. */

  function selectorFor(node, root) {
    var parts = [];
    while (node && node !== root && node.tagName) {
      if (node.id) {
        parts.unshift("#" + node.id);
        return parts.join(" > ");
      }
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

  function nearestSection(node, root) {
    while (node && node !== root && node.tagName) {
      var sibling = node.previousElementSibling;
      while (sibling) {
        var heading = /^H[1-6]$/.test(sibling.tagName)
          ? sibling
          : sibling.querySelector && sibling.querySelector("h1, h2, h3, h4, h5, h6");
        if (heading) return heading.textContent.replace(/\s+/g, " ").trim().slice(0, 120);
        sibling = sibling.previousElementSibling;
      }
      // A diff file (and many mocks) label their region with a <header>
      // rather than a heading element.
      var own = node.querySelector && node.querySelector(":scope > header");
      if (own) return own.textContent.replace(/\s+/g, " ").trim().slice(0, 120);
      node = node.parentElement;
    }
    return "";
  }

  function snippet(node) {
    return (node.textContent || "").replace(/\s+/g, " ").trim().slice(0, 200);
  }

  function whereLabel(section, selector) {
    if (section && selector) return section + "  ·  " + selector;
    return section || selector || "";
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
    // HTML artifacts are annotated by the #annotate-overlay handler below,
    // which records viewport coordinates; let that handler run instead of
    // swallowing the event and recording a meaningless overlay selector.
    if (event.target.closest("#annotate-overlay")) return;
    event.preventDefault();
    event.stopPropagation();
    var target = event.target.closest(annotateSelector) || event.target;
    target.classList.remove("sc-hl");
    target.classList.add("sc-marked");
    var selector = selectorFor(target, contentEl);
    var section = nearestSection(target, contentEl);
    openPopover(event.clientX, event.clientY, snippet(target), whereLabel(section, selector), function (text) {
      postFeedback({
        type: "annotation",
        text: text,
        selector: selector,
        section: section,
        quote: snippet(target)
      }).catch(showError);
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
      var context = "viewport " + Math.round(rect.width) + "px @ (" + x + ", " + y + ")";
      openPopover(event.clientX, event.clientY, "", context, function (text) {
        postFeedback({ type: "annotation", text: text, context: context }).catch(showError);
      });
    });
  }

  /* Text-selection comments. The affordance appears at the selection the
     moment it exists, in whichever document holds it: server-rendered
     content here, or the sandboxed HTML mock, which forwards its selection
     because the parent cannot read across an opaque origin. */

  function hideSelectionBtn() {
    if (selectionBtn) { selectionBtn.remove(); selectionBtn = null; }
  }

  function offerSelectionComment(anchor) {
    hideSelectionBtn();
    selectionBtn = el("button", "", "Comment");
    selectionBtn.id = "selection-btn";
    selectionBtn.style.left = Math.max(8, Math.min(anchor.left, window.innerWidth - 120)) + "px";
    selectionBtn.style.top = Math.max(8, Math.min(anchor.bottom + 6, window.innerHeight - 40)) + "px";
    var opened = false;
    function activate() {
      if (opened) return;
      opened = true;
      var x = anchor.left;
      var y = anchor.bottom + 6;
      hideSelectionBtn();
      openPopover(x, y, anchor.quote, whereLabel(anchor.section, anchor.selector), function (text) {
        postFeedback({
          type: "selection",
          text: text,
          quote: anchor.quote,
          selector: anchor.selector,
          section: anchor.section,
          context: anchor.context || ""
        }).catch(showError);
      });
    }
    // Commit on press, not on release: the document mouseup handler below
    // retires the affordance, so waiting for `click` would mean clicking an
    // element that is already gone. Suppressing the default also keeps the
    // text selection alive instead of letting the press collapse it. The
    // click binding is what a keyboard activation arrives as.
    selectionBtn.addEventListener("mousedown", function (event) {
      event.preventDefault();
      event.stopPropagation();
      activate();
    });
    selectionBtn.addEventListener("click", activate);
    document.body.appendChild(selectionBtn);
  }

  function readSelection() {
    hideSelectionBtn();
    if (annotating || popover) return;
    var sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount || !contentEl.contains(sel.anchorNode)) return;
    var quote = sel.toString().replace(/\s+/g, " ").trim();
    if (!quote) return;
    var range = sel.getRangeAt(0);
    var node = range.commonAncestorContainer;
    if (node && node.nodeType === 3) node = node.parentElement;
    var rect = range.getBoundingClientRect();
    offerSelectionComment({
      quote: quote.slice(0, 500),
      selector: node ? selectorFor(node, contentEl) : "",
      section: node ? nearestSection(node, contentEl) : "",
      left: rect.left,
      bottom: rect.bottom
    });
  }

  document.addEventListener("mouseup", function () { setTimeout(readSelection, 0); });
  document.addEventListener("keyup", function (event) {
    if (event.shiftKey || event.key === "Shift" || (event.ctrlKey || event.metaKey) && event.key === "a") {
      setTimeout(readSelection, 0);
    }
  });
  document.addEventListener("mousedown", function (event) {
    if (selectionBtn && event.target !== selectionBtn) hideSelectionBtn();
  });

  window.addEventListener("message", function (event) {
    var frame = document.getElementById("frame");
    // The mock runs in an opaque origin, so identity of the source window is
    // the only trustworthy check available.
    if (!frame || event.source !== frame.contentWindow) return;
    var data = event.data;
    if (!data || data.__showcase !== "selection") return;
    if (annotating || popover || !data.quote) { hideSelectionBtn(); return; }
    var rect = frame.getBoundingClientRect();
    offerSelectionComment({
      quote: String(data.quote).slice(0, 500),
      selector: String(data.selector || ""),
      section: String(data.section || ""),
      context: "html mock",
      left: rect.left + Number(data.left || 0),
      bottom: rect.top + Number(data.bottom || 0)
    });
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
      var dark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
      window.mermaid.initialize({ startOnLoad: false, theme: dark ? "dark" : "neutral" });
      window.mermaid.run({ querySelector: ".mermaid" }).catch(showFallback);
    } else {
      showFallback();
    }
  }

  refresh();
  pollTimer = setInterval(refresh, 2000);
})();
