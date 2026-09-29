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

  /* ---------------------------------------------------------------------------
     stream — camera stream viewer helper

     Open a WebSocket to /stream/<mac>, paint JPEG binary frames into an img
     element using blob URLs, handle reconnect with bounded exponential backoff,
     and pause while the tab is hidden to avoid starving the chip's /capture.
     ------------------------------------------------------------------------ */
  var stream = (function () {
    /* Reconnect delays in ms: 1, 2, 4, 8, 16 seconds. */
    var RECONNECT_DELAYS = [1000, 2000, 4000, 8000, 16000];
    var MAX_ATTEMPTS = RECONNECT_DELAYS.length;

    /* Guard browser globals at module level so the file can be loaded in Node. */
    var hasDocument = typeof document !== "undefined";
    var hasLocation = typeof location !== "undefined";
    var hasURL = typeof URL !== "undefined";
    var hasBlob = typeof Blob !== "undefined";

    /**
     * Derive the WebSocket URL from the current page origin.
     * Uses ws:// for http: pages, wss:// for https: pages.
     * @param {string} mac
     * @returns {string}
     */
    function buildWsUrl(mac) {
      /* The socket is always same-origin, so it is derived from the page and
       * never hardcoded. Outside a browser there is no page origin and this is
       * a programming error, not something to paper over with a default. */
      if (!hasLocation || !location.host) {
        throw new Error("witsaba.stream: no page origin to derive a socket URL from");
      }
      var wsProtocol = location.protocol === "https:" ? "wss:" : "ws:";
      return wsProtocol + "//" + location.host + "/stream/" + mac;
    }

    /**
     * Revoke a blob URL if it is still valid. Silently ignores invalid URLs.
     * @param {string|null} blobUrl
     */
    function revokeSafe(blobUrl) {
      if (!blobUrl || !hasURL) return;
      try {
        URL.revokeObjectURL(blobUrl);
      } catch (e) {
        /* revokeObjectURL throws on non-blob URLs; ignore. */
      }
    }

    /**
     * Create a blob URL for a JPEG binary frame and revoke the previous one.
     * @param {Uint8Array|ArrayBuffer} data
     * @param {string|null} prevUrl
     * @returns {string} the new blob URL
     */
    function swapBlobUrl(data, prevUrl) {
      revokeSafe(prevUrl);
      if (!hasBlob) return "";
      var blob = new Blob([data], { type: "image/jpeg" });
      return URL.createObjectURL(blob);
    }

    /**
     * Open a camera stream and paint frames into the target img element.
     *
     * @param {object} opts
     * @param {string}   opts.mac       - MAC address of the camera
     * @param {HTMLImageElement} opts.image - img element to paint frames into
     * @param {function} [opts.onStatus] - (state, detail) => void
     * @param {function} [opts.onFrame]  - (stats) => void, stats = { frames }
     * @param {function} [opts.onError]  - (Error) => void
     * @returns {{ close: function }} controller
     */
    function open(opts) {
      /* ---- validate required args ----------------------------------------- */
      if (!opts || typeof opts.mac !== "string" || !opts.mac) {
        throw new Error("witsaba.stream.open: mac is required");
      }
      if (!opts.image || typeof opts.image.src === "undefined") {
        throw new Error("witsaba.stream.open: image element is required");
      }

      var mac = opts.mac;
      var imageEl = opts.image;
      var onStatus = typeof opts.onStatus === "function" ? opts.onStatus : null;
      var onFrame = typeof opts.onFrame === "function" ? opts.onFrame : null;
      var onError = typeof opts.onError === "function" ? opts.onError : null;

      /* ---- internal state ----------------------------------------------- */
      var ws = null;
      var currentBlobUrl = null;
      var frameCount = 0;
      var closed = false;
      var reconnectAttempt = 0;
      var reconnectTimer = null;
      var isHidden = hasDocument ? !!document.hidden : false;

      /* ---- status emitter ------------------------------------------------ */
      function emitStatus(state, detail) {
        if (closed) return;
        if (onStatus) {
          onStatus(state, detail);
        }
      }

      /* ---- visibility handling ------------------------------------------ */
      function handleVisibilityChange() {
        if (!hasDocument) return;
        isHidden = !!document.hidden;
        if (isHidden) {
          /* Page hidden: close socket immediately so the chip is not starved. */
          closeSocket();
        } else {
          /* Page visible: reconnect if we were previously connected/live. */
          if (!closed && !ws) {
            scheduleReconnect(0);
          }
        }
      }

      if (hasDocument) {
        document.addEventListener("visibilitychange", handleVisibilityChange);
      }

      /* ---- socket management -------------------------------------------- */
      function closeSocket() {
        if (ws) {
          ws.onopen = null;
          ws.onmessage = null;
          ws.onerror = null;
          ws.onclose = null;
          try {
            ws.close();
          } catch (e) {
            /* ignore */
          }
          ws = null;
        }
      }

      function scheduleReconnect(delayMs) {
        cancelReconnect();
        if (closed) return;
        if (isHidden) return;
        reconnectTimer = setTimeout(function () {
          reconnectTimer = null;
          if (!closed && !isHidden) {
            openSocket();
          }
        }, delayMs);
      }

      function cancelReconnect() {
        if (reconnectTimer !== null) {
          clearTimeout(reconnectTimer);
          reconnectTimer = null;
        }
      }

      /* ---- frame painting ------------------------------------------------ */
      function paintFrame(data) {
        /* Create and assign new blob URL, revoking the previous one. */
        var newUrl = swapBlobUrl(data, currentBlobUrl);
        if (newUrl) {
          currentBlobUrl = newUrl;
          imageEl.src = newUrl;
          frameCount++;
          /* "live" means a frame is on screen, not merely that a socket
           * opened. See the onopen handler. */
          if (frameCount === 1) {
            emitStatus("live", null);
          }
          if (onFrame) {
            onFrame({ frames: frameCount });
          }
        }
      }

      /* ---- message handler ---------------------------------------------- */
      function handleMessage(event) {
        /* Only binary frames are JPEGs. The chip sends a JSON text hello frame
         * on connect; ignore it silently. */
        if (typeof event.data === "string") {
          /* Text frame — ignore (do not paint, do not throw). */
          return;
        }

        /* Binary frame — must be a JPEG. The socket is opened with
         * binaryType "arraybuffer" (see openSocket), so event.data is already
         * an ArrayBuffer and paintFrame runs synchronously in arrival order.
         *
         * Do NOT route this through FileReader. That detour made painting
         * asynchronous and unbounded: several FileReader callbacks could be in
         * flight at 10 fps, so a later frame could paint before an earlier one
         * and the viewer would visibly reorder. Synchronous painting also keeps
         * exactly one frame outstanding, so there is natural backpressure. */
        paintFrame(event.data);
      }

      /* ---- socket error handling ---------------------------------------- */
      function handleError(event) {
        /* An error event is ALWAYS followed by a close event (WebSocket spec),
         * and handleClose is the single reconnect trigger. Reconnecting here
         * too would consume two entries of the retry ladder per single drop,
         * exhausting the 1s/2s/4s/8s/16s budget twice as fast. So: report,
         * and let handleClose do the reconnecting. */
        if (closed) return;
        emitStatus("error", null);
        if (onError) {
          onError(new Error("WebSocket error"));
        }
      }

      function handleClose(event) {
        closeSocket();
        if (closed) return;
        attemptReconnect();
      }

      function attemptReconnect() {
        if (closed) return;
        if (isHidden) return;

        if (reconnectAttempt >= MAX_ATTEMPTS) {
          /* Exhausted retries — stop and report permanent error. */
          emitStatus("error", "max retries exceeded");
          closed = true;
          cleanup();
          return;
        }

        emitStatus("reconnecting", null);
        var delay = RECONNECT_DELAYS[reconnectAttempt] || RECONNECT_DELAYS[MAX_ATTEMPTS - 1];
        reconnectAttempt++;
        scheduleReconnect(delay);
      }

      /* ---- open socket -------------------------------------------------- */
      function openSocket() {
        if (closed) return;
        if (isHidden) return;

        closeSocket();
        emitStatus("connecting", null);

        var url = buildWsUrl(mac);
        try {
          ws = new WebSocket(url);
          /* Arraybuffer (not the default Blob) so handleMessage can paint
           * synchronously and in order. See handleMessage. */
          ws.binaryType = "arraybuffer";
        } catch (e) {
          emitStatus("error", e && e.message ? e.message : "WebSocket unavailable");
          if (onError) onError(e || new Error("WebSocket unavailable"));
          attemptReconnect();
          return;
        }

        ws.onopen = function () {
          /* A successful connection resets the ladder, but "live" is NOT
           * reported here: the socket can open and then never deliver a frame
           * (chip busy, single-viewer refused, capture wedged). "live" is
           * emitted by paintFrame on the first frame that actually lands. */
          reconnectAttempt = 0;
        };

        ws.onmessage = handleMessage;
        ws.onerror = handleError;
        ws.onclose = handleClose;
      }

      /* ---- cleanup (shared between close and error paths) --------------- */
      function cleanup() {
        /* Remove visibility listener. */
        if (hasDocument) {
          document.removeEventListener("visibilitychange", handleVisibilityChange);
        }

        /* Cancel any pending reconnect. */
        cancelReconnect();

        /* Close the socket. */
        closeSocket();

        /* Revoke the final blob URL so the last frame does not leak. */
        revokeSafe(currentBlobUrl);
        currentBlobUrl = null;
      }

      /* ---- controller API ----------------------------------------------- */
      var controller = {
        close: function () {
          if (closed) return; /* idempotent */
          closed = true;
          emitStatus("stopped", null);
          cleanup();
        },
      };

      /* Start immediately if page is visible. */
      if (!isHidden) {
        openSocket();
      }

      return controller;
    }

    /* ---- public API ------------------------------------------------------ */
    return {
      open: open,
    };
  })();

  global.witsaba = {
    api: api,
    relativeTime: relativeTime,
    el: el,
    setStatus: setStatus,
    showState: showState,
    onVisible: onVisible,
    stream: stream,
  };
})(window);
