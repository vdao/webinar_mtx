/* Admin panel niceties: copy buttons, confirmations, live viewers, presets. */
(function () {
  "use strict";

  // confirm destructive actions
  document.addEventListener("submit", function (ev) {
    var btn = ev.target.querySelector("button[data-confirm]");
    if (btn && !window.confirm(btn.dataset.confirm)) {
      ev.preventDefault();
    }
  });

  // copy buttons
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-copy]");
    if (!btn) return;
    var el = document.querySelector(btn.dataset.copy);
    if (!el) return;
    var text = el.textContent.trim();
    var done = function () {
      var t = btn.textContent;
      btn.textContent = "Скопировано ✓";
      setTimeout(function () { btn.textContent = t; }, 1500);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { fallback(); });
    } else {
      fallback();
    }
    function fallback() {
      var ta = document.createElement("textarea");
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
      done();
    }
  });

  // live viewers polling on the webinar edit page
  var stats = document.getElementById("live-stats");
  if (stats && window.WEBINAR_ID) {
    var load = function () {
      fetch("/admin/webinars/" + window.WEBINAR_ID + "/viewers", { headers: { "Accept": "application/json" } })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d.online) {
            stats.textContent = "● В эфире · зрителей: " + d.viewers;
            stats.classList.add("online");
          } else {
            stats.textContent = "○ Нет эфира";
            stats.classList.remove("online");
          }
        })
        .catch(function () {});
    };
    load();
    setInterval(load, 15000);
  }

  // webhook presets fill paths + sample payload
  document.querySelectorAll(".preset").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var emailInput = document.querySelector('input[name="email_path"]');
      var productInput = document.querySelector('input[name="product_path"]');
      var payload = document.getElementById("test-payload");
      if (emailInput && btn.dataset.email) emailInput.value = btn.dataset.email;
      if (productInput && btn.dataset.product) productInput.value = btn.dataset.product;
      if (payload && btn.dataset.sample) payload.value = btn.dataset.sample;
    });
  });
})();
