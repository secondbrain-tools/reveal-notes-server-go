/**
 * remote-notes-client
 *
 * Connects a presentation-compatible slide runtime to the remote-notes-server.
 *
 * Usage options:
 * 1. Publisher/runtime mode via manifest placeholders:
 *      {env.NOTES_SERVER_URL}
 *      {env.REMOTE_NOTES_ACCESS_TOKEN}
 * 2. Plain HTML/demo mode via:
 *      window.REMOTE_NOTES_CLIENT_CONFIG = { serverUrl, token, socketId, socketIoPath, revealConfig }
 *      window.RemoteNotesClient.init({ ... })
 */
(function (global) {
  function unresolved(value) {
    return !value || /^\{.+\}$/.test(String(value));
  }

  function pick(value, fallback) {
    return unresolved(value) ? fallback : value;
  }

  function loadScript(src, done) {
    if (!src) return done(new Error("Missing socket.io script path"));
    if (global.io) return done();
    var script = document.createElement("script");
    script.src = src;
    script.onerror = function () {
      done(new Error("Failed to load " + src));
    };
    script.onload = function () {
      done();
    };
    document.head.appendChild(script);
  }

  function createClient(config) {
    var reveal = config.reveal || global.Reveal;
    var revealConfig = config.revealConfig || {};
    if (!reveal) {
      console.warn("RemoteNotesClient: Reveal-compatible API not found");
      return null;
    }

    var token = config.token || "";
    var serverUrl = config.serverUrl;
    var socketId = config.socketId || Math.random().toString(36).slice(2);
    var opts = { query: { socketId: socketId } };
    if (token) {
      opts.auth = { token: token };
      opts.query.token = token;
    }
    opts.withCredentials = true;

    var socket = global.io.connect(serverUrl, opts);
    var notesUrl =
      serverUrl.replace(/\/+$/, "") + "/notes/" + encodeURIComponent(socketId);

    function emitStatus(type, detail) {
      document.dispatchEvent(
        new CustomEvent("remote-notes-client:" + type, {
          detail: detail || {},
        }),
      );
    }

    function post() {
      var slide = reveal.getCurrentSlide ? reveal.getCurrentSlide() : null;
      var notesEl = slide ? slide.querySelector("aside.notes") : null;
      var data = {
        notes: "",
        markdown: false,
        socketId: socketId,
        state: reveal.getState ? reveal.getState() : {},
      };

      if (slide && slide.hasAttribute("data-notes")) {
        data.notes = slide.getAttribute("data-notes");
      }
      if (notesEl) {
        data.notes = notesEl.innerHTML;
        data.markdown = !!notesEl.getAttribute("data-markdown");
      }
      socket.emit("statechanged", data);
      emitStatus("state", data);
    }

    function bestEffortCookieAuth() {
      if (!token) return;
      try {
        fetch(serverUrl.replace(/\/+$/, "") + "/auth-token", {
          method: "POST",
          mode: "cors",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token: token }),
        }).catch(function () {});
      } catch (e) {}
    }

    socket.on("connect", function () {
      console.log("%cSpeaker notes%c " + notesUrl, "font-weight:bold", "");
      emitStatus("connected", {
        serverUrl: serverUrl,
        socketId: socketId,
        notesUrl: notesUrl,
      });
      post();
    });

    socket.on("connect_error", function (err) {
      console.warn(
        "Speaker notes: connection refused (" +
          (err && err.message ? err.message : err) +
          ")",
      );
      emitStatus("error", {
        error: err,
        serverUrl: serverUrl,
        socketId: socketId,
        notesUrl: notesUrl,
      });
    });

    socket.on("disconnect", function (reason) {
      emitStatus("disconnected", {
        reason: reason,
        serverUrl: serverUrl,
        socketId: socketId,
        notesUrl: notesUrl,
      });
    });

    socket.on("new-subscriber", post);
    socket.on("statechanged-speaker", function (d) {
      if (reveal.setState) reveal.setState(d.state);
    });

    bestEffortCookieAuth();

    function bindReveal() {
      var ready = reveal.initialize ? reveal.initialize(revealConfig) : Promise.resolve();
      Promise.resolve(ready).then(function () {
        if (reveal.on) {
          reveal.on("slidechanged", post);
          reveal.on("fragmentshown", post);
          reveal.on("fragmenthidden", post);
          reveal.on("overviewhidden", post);
          reveal.on("overviewshown", post);
        }
        post();
      });
    }

    bindReveal();

    return {
      socket: socket,
      socketId: socketId,
      notesUrl: notesUrl,
      post: post,
    };
  }

  var api = {
    init: function (options) {
      var config = options || {};
      var injectedConfig = global.REMOTE_NOTES_CLIENT_CONFIG || {};
      var serverUrl =
        config.serverUrl ||
        injectedConfig.serverUrl ||
        pick("{env.NOTES_SERVER_URL}", "");
      var token =
        config.token ||
        injectedConfig.token ||
        pick(
          "{env.REMOTE_NOTES_ACCESS_TOKEN}",
          pick("{env.ACCESS_TOKEN}", ""),
        ) ||
        "";
      var socketIoPath =
        config.socketIoPath ||
        injectedConfig.socketIoPath ||
        "./libs/runtime/remote-notes-client/socket.io.js";

      var revealConfig =
        config.revealConfig || injectedConfig.revealConfig || {};

      if (!serverUrl) {
        console.warn("RemoteNotesClient: no serverUrl configured");
        return Promise.resolve(null);
      }

      return new Promise(function (resolve, reject) {
        loadScript(socketIoPath, function (err) {
          if (err) return reject(err);
          try {
            resolve(
              createClient({
                reveal: config.reveal || injectedConfig.reveal,
                serverUrl: serverUrl,
                token: token,
                socketId: config.socketId || injectedConfig.socketId,
                revealConfig: revealConfig,
              }),
            );
          } catch (e) {
            reject(e);
          }
        });
      });
    },
  };

  global.RemoteNotesClient = api;

  if (
    document.currentScript &&
    document.currentScript.hasAttribute("data-auto-init")
  ) {
    api.init().catch(function (err) {
      console.warn("RemoteNotesClient init failed", err);
    });
    return;
  }

  if (!unresolved("{env.NOTES_SERVER_URL}")) {
    api.init().catch(function (err) {
      console.warn("RemoteNotesClient init failed", err);
    });
  }
})(window);
