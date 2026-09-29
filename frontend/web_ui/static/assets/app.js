/* =============================================================================
   witsaba web ui - static helpers

   Dependency-free, no build step, no framework. Loaded as a classic script,
   not a module, so the pages work from file:// as well as through nginx.

   Same-origin by design: every request goes to a relative path such as
   /api/devices/active, which nginx proxies to messaging-core. There is no
   API base URL to configure and no CORS involved.
   ========================================================================== */

(function (global) {
  "use strict";

  /* ---------------------------------------------------------------------------
     api(path, options)

     Fetch a JSON endpoint with a hard timeout. Returns parsed JSON, or throws
     an Error with a message worth showing an operator.
     ------------------------------------------------------------------------ */
  function api(path, options) {
    var opts = options || {};
    var timeoutMs = opts.timeoutMs || 5000;
    var controller =
      typeof AbortController !== "undefined" ? new AbortController() : null;
    var timer = null;

    if (controller) {
      timer = setTimeout(function () {
        controller.abort();
      }, timeoutMs);
    }

    return fetch(path, {
      signal: controller ? controller.signal : undefined,
      cache: "no-store",
      headers: { Accept: "application/json" },
    })
      .then(function (res) {
        if (!res.ok) {
          throw new Error("API returned " + res.status + " " + res.statusText);
        }
        return res.json();
      })
      .finally(function () {
        if (timer) clearTimeout(timer);
      });
  }

  /* ---------------------------------------------------------------------------
     relativeTime(iso)

     Ported from the Qwik app's devices route so the wording matches.
     ------------------------------------------------------------------------ */
  function relativeTime(iso) {
    var then = new Date(iso).getTime();
    if (isNaN(then)) return "unknown";

    var diff = Date.now() - then;
    if (diff < 0) return "in the future";

    var sec = Math.floor(diff / 1000);
    if (sec < 10) return "just now";
    if (sec < 60) return sec + "s ago";

    var min = Math.floor(sec / 60);
    if (min < 60) return min + "m ago";

    var hr = Math.floor(min / 60);
    if (hr < 24) return hr + "h ago";

    return Math.floor(hr / 24) + "d ago";
  }

  /* ---------------------------------------------------------------------------
     el(tag, attrs, children)

     Minimal element builder. textContent is used throughout, never innerHTML,
     so device names and MACs coming off the LAN cannot inject markup.
     ------------------------------------------------------------------------ */
  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    var key;

    if (attrs) {
      for (key in attrs) {
        if (!Object.prototype.hasOwnProperty.call(attrs, key)) continue;
        var value = attrs[key];
        if (value === null || value === undefined || value === false) continue;
        if (key === "text") {
          node.textContent = String(value);
        } else if (key === "class") {
          node.className = value;
        } else {
          node.setAttribute(key, value === true ? "" : String(value));
        }
      }
    }

    if (children) {
      for (var i = 0; i < children.length; i++) {
        var child = children[i];
        if (child === null || child === undefined || child === false) continue;
        node.appendChild(
          typeof child === "string" ? document.createTextNode(child) : child,
        );
      }
    }

    return node;
  }

  /* ---------------------------------------------------------------------------
     setStatus(node, state, label, pulse)

     Update a .status-chip in place. state is healthy|warning|error|info|neutral.
     ------------------------------------------------------------------------ */
  function setStatus(node, state, label, pulse) {
    if (!node) return;
    node.className =
      "status-chip status-chip--" + state + (pulse ? " status-chip--pulse" : "");
    var labelNode = node.querySelector("[data-status-label]");
    if (labelNode) labelNode.textContent = label;
  }

  /* ---------------------------------------------------------------------------
     showState(container, variant, title, body, detail)

     Replace a container's contents with a single empty/error state block.
     ------------------------------------------------------------------------ */
  function showState(container, variant, title, body, detail) {
    container.replaceChildren(
      el("div", { class: "state state--" + variant, role: "status" }, [
        el("p", { class: "state__title", text: title }),
        body ? el("p", { class: "state__body", text: body }) : null,
        detail
          ? el("p", { class: "state__error__detail", text: detail })
          : null,
      ]),
    );
  }

  /* ---------------------------------------------------------------------------
     onVisible(callback, intervalMs)

     Run now, then on an interval. Polling pauses while the tab is hidden and
     resumes immediately on return, so a background tab does not hammer the Pi.
     ------------------------------------------------------------------------ */
  function onVisible(callback, intervalMs) {
    var timer = null;

    function tick() {
      callback();
    }

    function start() {
      if (timer !== null) return;
      tick();
      timer = setInterval(tick, intervalMs);
    }

    function stop() {
      if (timer === null) return;
      clearInterval(timer);
      timer = null;
    }

    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", function () {
        if (document.hidden) {
          stop();
        } else {
          start();
        }
      });
    }

    start();
  }

  global.witsaba = {
    api: api,
    relativeTime: relativeTime,
    el: el,
    setStatus: setStatus,
    showState: showState,
    onVisible: onVisible,
  };
})(window);
