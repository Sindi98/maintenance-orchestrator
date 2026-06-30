// Minimal, dependency-free live refresh. Every element with a data-src attribute
// has its innerHTML replaced with the HTML fragment fetched from that URL.
(function () {
  "use strict";

  var INTERVAL = 3000;

  function refresh(el) {
    fetch(el.getAttribute("data-src"), { headers: { "X-Requested-With": "fetch" } })
      .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
      .then(function (html) {
        // Avoid clobbering a control the user is interacting with.
        if (document.activeElement && el.contains(document.activeElement)) return;
        el.innerHTML = html;
      })
      .catch(function () { /* transient errors are ignored; next tick retries */ });
  }

  function tick() {
    document.querySelectorAll("[data-src]").forEach(refresh);
  }

  document.addEventListener("DOMContentLoaded", function () {
    if (document.querySelector("[data-src]")) {
      setInterval(tick, INTERVAL);
    }
  });
})();
